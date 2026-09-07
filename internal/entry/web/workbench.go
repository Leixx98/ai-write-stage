package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Leixx98/ai-write-stage/assets"
	"github.com/Leixx98/ai-write-stage/internal/bootstrap"
	"github.com/Leixx98/ai-write-stage/internal/host"
	"github.com/Leixx98/ai-write-stage/internal/store"
	"github.com/Leixx98/ai-write-stage/internal/workspace"
)

var errWorkspaceBusy = errors.New("写作或独占作业进行中，请先暂停再切换工作区")

type workbench struct {
	mu      sync.Mutex
	cfg     bootstrap.Config
	root    string
	name    string
	rt      *host.Host
	ctrl    *v2Controller
	events  *eventBroker
	streams *streamBroker
}

func newWorkbench(cfg bootstrap.Config, root string) *workbench {
	wb := &workbench{
		cfg:     cfg,
		root:    root,
		events:  newEventBroker(),
		streams: newStreamBroker(),
	}
	wb.ctrl = newV2Controller(nil)
	wb.ctrl.wb = wb
	wb.ctrl.root = root
	return wb
}

func (wb *workbench) close() {
	wb.mu.Lock()
	rt := wb.rt
	wb.rt = nil
	wb.name = ""
	if wb.ctrl != nil {
		wb.ctrl.attach(nil, "")
	}
	wb.mu.Unlock()
	if rt != nil {
		rt.Close()
	}
}

func (wb *workbench) open(name string) error {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("工作区名不能为空: %w", workspace.ErrInvalidName)
	}
	if wb.rt != nil && workspace.NamesEqual(wb.name, name) {
		return nil
	}
	if err := workspaceBusyError(wb.busyLocked()); err != nil {
		return err
	}
	info, err := workspace.Lookup(wb.root, name)
	if err != nil {
		return err
	}
	old := wb.rt
	wb.rt = nil
	wb.ctrl.attach(nil, "")
	if old != nil {
		old.Close()
	}
	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		return err
	}
	cfg, err = bootstrap.ApplyWorkspaceDir(cfg, info.Path)
	if err != nil {
		return err
	}
	rt, err := host.New(cfg, loadBookBundle(cfg), host.WithFileLog("runtime.log", false))
	if err != nil {
		return err
	}
	wb.rt = rt
	wb.name = info.Name
	wb.ctrl.root = wb.root
	wb.ctrl.attach(rt, info.Name)
	wb.events.follow(rt)
	wb.streams.follow(rt)
	if err := workspace.SaveLast(wb.root, info.Name); err != nil {
		slog.Warn("保存上次工作区失败", "workspace", info.Name, "err", err)
	}
	return nil
}

func (wb *workbench) busyLocked() (running bool, exclusive string) {
	if wb.rt == nil {
		return false, ""
	}
	snap := wb.rt.Snapshot()
	return snap.IsRunning, snap.Exclusive
}

func workspaceBusyError(running bool, exclusive string) error {
	if running {
		return errWorkspaceBusy
	}
	if strings.TrimSpace(exclusive) != "" {
		return fmt.Errorf("当前正在%s，请先暂停再切换工作区: %w", exclusive, errWorkspaceBusy)
	}
	return nil
}

func loadBookBundle(cfg bootstrap.Config) assets.Bundle {
	bundle := assets.Load(cfg.Style, assets.DefaultLoadOptions(cfg.OutputDir))
	if overrides, err := assets.LoadPromptOverrides(cfg.OutputDir); err != nil {
		slog.Warn("加载提示词覆盖失败，使用内置提示词", "err", err)
	} else {
		assets.ApplyPromptOverrides(&bundle, overrides)
	}
	return bundle
}

func (c *v2Controller) attach(rt *host.Host, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.name = name
	c.rt = rt
	if rt == nil {
		c.st = nil
		c.images = nil
		c.tavern = nil
		c.chat = nil
		c.svc = nil
		return
	}
	roots := rt.Roots()
	c.st = roots.Facts
	c.images = roots.Images
	c.imageConfig = roots.ImageConfig
	c.comfy = roots.ComfyUI
	c.tavern = roots.Tavern
	c.chat = rt.NewGalgameGenerate()
	c.svc = rt.ImageService()
}

func (c *v2Controller) initMachineStores() {
	roots := store.Open(filepath.Join(os.TempDir(), "ainovel-unbound"), "")
	c.imageConfig = roots.ImageConfig
	c.comfy = roots.ComfyUI
}

func (c *v2Controller) requireHost(w http.ResponseWriter) bool {
	c.mu.Lock()
	rt := c.rt
	c.mu.Unlock()
	if rt != nil {
		return true
	}
	envelopeErr(w, 409, codeConflict, fmt.Errorf("请先选择工作区"))
	return false
}

func isKnownBookAPI(p string) bool {
	switch {
	case p == "chapters", strings.HasPrefix(p, "chapters/"):
		return true
	case p == "export", strings.HasPrefix(p, "commands/"):
		return true
	case strings.HasPrefix(p, "import/"):
		return true
	case strings.HasPrefix(p, "settings/"):
		return true
	case strings.HasPrefix(p, "galgame/"):
		return true
	case p == "image-jobs", strings.HasPrefix(p, "image-jobs/"):
		return true
	case strings.HasPrefix(p, "units/"):
		return true
	case strings.HasPrefix(p, "image-generation/providers"):
		return true
	default:
		return false
	}
}
