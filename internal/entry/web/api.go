package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/voocel/ainovel-cli/internal/entry/startup"
	"github.com/voocel/ainovel-cli/internal/galgame"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/host/exp"
	"github.com/voocel/ainovel-cli/internal/host/imp"
	imagesvc "github.com/voocel/ainovel-cli/internal/imagejob/service"
	"github.com/voocel/ainovel-cli/internal/store"
)

const (
	codeInvalidRequest  = 1001
	codeNotFound        = 1002
	codeConflict        = 1003
	codeConfigInvalid   = 2001
	codeUnreachable     = 3001
	codeWorkflowInvalid = 3002
	codeJobFailed       = 3003
	codeJobTimeout      = 3004
	codeJobCancelled    = 3005
	codePromptSchema    = 3101
	codePrompterJSON    = 3102
	codePrompterCall    = 3103
	codePrompterTimeout = 3104
	codeUnitJobConflict = 3105
)

type apiEnvelope struct {
	Code int    `json:"code"`
	Data any    `json:"data"`
	Msg  string `json:"msg"`
}

func envelope(w http.ResponseWriter, status, code int, data any, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiEnvelope{code, data, msg})
}
func envelopeErr(w http.ResponseWriter, status, code int, err error) {
	envelope(w, status, code, map[string]any{}, err.Error())
}

type v2Controller struct {
	rt          *host.Host
	st          *store.Store
	images      *store.ImageStore
	imageConfig *store.ImageConfigStore
	comfy       *store.ComfyUIStore
	tavern      *store.GalgameStore
	svc         *imagesvc.Service
	chat        galgame.StreamFunc
	mu          sync.Mutex
}

func newV2Controller(rt *host.Host) *v2Controller {
	roots := rt.Roots()
	st := roots.Facts
	c := &v2Controller{rt: rt, st: st, images: roots.Images, imageConfig: roots.ImageConfig, comfy: roots.ComfyUI, tavern: roots.Tavern, chat: rt.NewGalgameGenerate()}
	c.svc = rt.ImageService()
	return c
}

func registerV2(mux *http.ServeMux, rt *host.Host) {
	c := newV2Controller(rt)
	mux.HandleFunc("/api/v2/", c.dispatch)
}

func (c *v2Controller) dispatch(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/api/v2/")
	switch {
	case p == "chapters":
		c.readerChapters(w, r)
	case strings.HasPrefix(p, "chapters/"):
		c.readerChapter(w, r, strings.TrimPrefix(p, "chapters/"))
	case p == "image-generation/settings":
		c.imageGenerationSettings(w, r)
	case p == "image-generation/profiles":
		c.imageProfiles(w, r, "")
	case strings.HasPrefix(p, "image-generation/profiles/"):
		c.imageProfiles(w, r, strings.TrimPrefix(p, "image-generation/profiles/"))
	case p == "image-generation/providers":
		c.imageProviders(w, r, "")
	case strings.HasPrefix(p, "image-generation/providers/") && !strings.HasPrefix(p, "image-generation/providers/comfyui/"):
		c.imageProviders(w, r, strings.TrimPrefix(p, "image-generation/providers/"))
	case p == "image-generation/providers/comfyui/config":
		c.config(w, r)
	case p == "galgame/characters":
		c.galgameCharacters(w, r)
	case p == "galgame/characters/import":
		c.importGalgameCharacter(w, r)
	case strings.HasPrefix(p, "galgame/characters/"):
		c.galgameCharacter(w, r, strings.TrimPrefix(p, "galgame/characters/"))
	case p == "galgame/plays":
		c.galgamePlays(w, r)
	case strings.HasPrefix(p, "galgame/plays/"):
		c.galgamePlay(w, r, strings.TrimPrefix(p, "galgame/plays/"))
	case p == "galgame/sessions":
		c.galgameSessions(w, r)
	case strings.HasPrefix(p, "galgame/sessions/"):
		c.galgameSession(w, r, strings.TrimPrefix(p, "galgame/sessions/"))
	case p == "image-generation/providers/comfyui/test-connection":
		c.testConnection(w, r)
	case p == "image-generation/providers/comfyui/prompter-presets":
		c.prompterPresets(w, r)
	case p == "image-generation/providers/comfyui/prompter/parse" && r.Method == http.MethodPost:
		c.parsePrompterJSON(w, r)
	case p == "image-generation/providers/comfyui/instances" && r.Method == http.MethodGet:
		c.instances(w, r)
	case p == "image-generation/providers/comfyui/instances" && r.Method == http.MethodPut:
		c.saveInstances(w, r)
	case strings.HasPrefix(p, "image-generation/providers/comfyui/instances/") && strings.HasSuffix(p, "/test"):
		c.testInstance(w, r, strings.TrimSuffix(strings.TrimPrefix(p, "image-generation/providers/comfyui/instances/"), "/test"))
	case p == "image-generation/providers/comfyui/media/upload" && r.Method == http.MethodPost:
		c.uploadMedia(w, r)
	case strings.HasPrefix(p, "image-generation/providers/comfyui/media/"):
		c.getMedia(w, r, strings.TrimPrefix(p, "image-generation/providers/comfyui/media/"))
	case p == "image-generation/providers/comfyui/workflows" && r.Method == http.MethodGet:
		c.listWorkflows(w, r)
	case p == "image-generation/providers/comfyui/workflows/import" && r.Method == http.MethodPost:
		c.importWorkflow(w, r)
	case strings.HasPrefix(p, "image-generation/providers/comfyui/workflows/"):
		c.workflow(w, r, strings.TrimPrefix(p, "image-generation/providers/comfyui/workflows/"))
	case p == "image-jobs":
		c.imageJobs(w, r)
	case strings.HasPrefix(p, "image-jobs/"):
		c.job(w, r, strings.TrimPrefix(p, "image-jobs/"))
	case strings.HasPrefix(p, "units/"):
		c.unit(w, r, strings.TrimPrefix(p, "units/"))
	case p == "state" && r.Method == http.MethodGet:
		envelope(w, http.StatusOK, 0, map[string]any{"snapshot": c.rt.Snapshot(), "workspace_id": webWorkspaceID(c.rt.Dir())}, "")
	case p == "replay" && r.Method == http.MethodGet:
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		items, err := c.rt.ReplayQueue(after)
		if err != nil {
			envelopeErr(w, 500, codeConflict, err)
			return
		}
		envelope(w, http.StatusOK, 0, items, "")
	case p == "export" && r.Method == http.MethodPost:
		c.exportBook(w, r)
	case p == "settings/models":
		c.settingsModels(w, r)
	case p == "settings/workflow":
		c.settingsDocument(w, r, "workflow")
	case p == "settings/prompts":
		c.settingsDocument(w, r, "prompts")
	case p == "import/status":
		c.importStatus(w, r)
	case p == "import/start":
		c.importStart(w, r)
	case p == "import/confirm":
		c.importConfirm(w, r)
	case p == "import/resegment":
		c.importResegment(w, r)
	case p == "import/resolve":
		c.importResolve(w, r)
	case p == "import/cancel":
		c.importCancel(w, r)
	case strings.HasPrefix(p, "commands/"):
		c.command(w, r, strings.TrimPrefix(p, "commands/"))
	default:
		envelopeErr(w, http.StatusNotFound, codeNotFound, fmt.Errorf("API route not found"))
	}
}

func webWorkspaceID(dir string) string {
	canonical, err := filepath.Abs(dir)
	if err != nil {
		canonical = filepath.Clean(dir)
	}
	canonical = filepath.ToSlash(filepath.Clean(canonical))
	if filepath.Separator == '\\' {
		canonical = strings.ToLower(canonical)
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:16])
}

func (c *v2Controller) exportBook(w http.ResponseWriter, r *http.Request) {
	format := exp.FormatEPUB
	if r.Body != nil {
		var req struct {
			Format string `json:"format"`
		}
		err := decodeBody(r, &req)
		if err != nil && err != io.EOF {
			envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, fmt.Errorf("invalid export request"))
			return
		}
		switch strings.ToLower(strings.TrimSpace(req.Format)) {
		case "", "epub":
			format = exp.FormatEPUB
		case "txt":
			format = exp.FormatTXT
		default:
			envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, fmt.Errorf("不支持的导出格式 %q", req.Format))
			return
		}
	}
	result, err := exp.Run(r.Context(), exp.Deps{Store: c.st}, exp.Options{Format: format, Overwrite: true})
	if err != nil {
		envelopeErr(w, http.StatusUnprocessableEntity, codeInvalidRequest, err)
		return
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		envelopeErr(w, http.StatusInternalServerError, codeConfigInvalid, fmt.Errorf("读取导出文件失败: %w", err))
		return
	}
	filename := filepath.Base(result.Path)
	contentType := "application/epub+zip"
	if format == exp.FormatTXT {
		contentType = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.QueryEscape(filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (c *v2Controller) command(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	var req struct {
		Prompt      string `json:"prompt"`
		Text        string `json:"text"`
		Direction   string `json:"direction"`
		Preferences string `json:"preferences"`
	}
	if r.Body != nil {
		_ = decodeBody(r, &req)
	}
	var err error
	switch name {
	case "start":
		plan, e := startup.PrepareQuick(startup.Request{Mode: startup.ModeQuick, UserPrompt: req.Prompt, OutputDir: c.rt.Dir(), Interactive: true})
		if e == nil {
			e = c.rt.PrepareUserRules(plan.RawPrompt)
		}
		if e == nil {
			e = c.rt.StartPrepared(plan.RawPrompt)
		}
		err = e
	case "continue":
		if strings.TrimSpace(req.Text) == "" {
			_, err = c.rt.Resume()
		} else {
			err = c.rt.Continue(req.Text)
		}
	case "steer":
		err = c.rt.Steer(req.Text)
	case "reopen":
		direction := strings.TrimSpace(req.Direction)
		if direction == "" {
			err = fmt.Errorf("continuation direction is required")
			break
		}
		if err = c.rt.Reopen(direction); err == nil {
			_, err = c.rt.Resume()
		}
	case "writing-rules":
		preferences := req.Preferences
		if strings.TrimSpace(preferences) == "" {
			preferences = req.Text
		}
		err = c.rt.ApplyWritingRules(preferences)
	case "abort", "pause":
		c.rt.Abort()
	default:
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("command %q not found", name))
		return
	}
	if err != nil {
		envelopeErr(w, 409, codeConflict, err)
		return
	}
	envelope(w, 202, 0, map[string]any{"accepted": true}, "")
}

func (c *v2Controller) importStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	envelope(w, http.StatusOK, 0, c.rt.ImportStatus(), "")
}

func (c *v2Controller) importStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	var req struct {
		SourcePath    string `json:"source_path"`
		StoryStatus   string `json:"story_status"`
		Guidance      string `json:"guidance"`
		ContinueAfter bool   `json:"continue_after"`
	}
	if err := decodeBody(r, &req); err != nil {
		envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, err)
		return
	}
	sourcePath := strings.TrimSpace(req.SourcePath)
	if sourcePath == "" {
		status := c.rt.ImportStatus()
		if !status.CanResume || status.State == host.ImportSessionAwaitingConfirmation || status.State == host.ImportSessionAwaitingStoryStatus {
			envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, fmt.Errorf("server-local source path is required"))
			return
		}
	} else if !filepath.IsAbs(sourcePath) {
		envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, fmt.Errorf("server-local source path must be absolute"))
		return
	}
	storyStatus := strings.ToLower(strings.TrimSpace(req.StoryStatus))
	if storyStatus == "undetermined" {
		storyStatus = ""
	}
	if storyStatus != "" && storyStatus != "open" && storyStatus != "closed" {
		envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, fmt.Errorf("story status must be open, closed, or undetermined"))
		return
	}
	if err := c.rt.StartImport(imp.Options{SourcePath: sourcePath, StoryResolution: storyStatus, Guidance: req.Guidance, ContinueAfter: req.ContinueAfter}); err != nil {
		envelopeErr(w, http.StatusConflict, codeConflict, err)
		return
	}
	envelope(w, http.StatusAccepted, 0, c.rt.ImportStatus(), "")
}

func (c *v2Controller) importConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	if err := c.rt.ConfirmImport(); err != nil {
		envelopeErr(w, http.StatusConflict, codeConflict, err)
		return
	}
	envelope(w, http.StatusAccepted, 0, c.rt.ImportStatus(), "")
}

func (c *v2Controller) importResegment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	var req struct {
		Guidance string `json:"guidance"`
	}
	if err := decodeBody(r, &req); err != nil {
		envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, err)
		return
	}
	if err := c.rt.ResegmentImport(req.Guidance); err != nil {
		envelopeErr(w, http.StatusConflict, codeConflict, err)
		return
	}
	envelope(w, http.StatusAccepted, 0, c.rt.ImportStatus(), "")
}

func (c *v2Controller) importResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	var req struct {
		StoryStatus string `json:"story_status"`
	}
	if err := decodeBody(r, &req); err != nil {
		envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, err)
		return
	}
	if err := c.rt.ResolveImportStory(req.StoryStatus); err != nil {
		envelopeErr(w, http.StatusConflict, codeConflict, err)
		return
	}
	envelope(w, http.StatusAccepted, 0, c.rt.ImportStatus(), "")
}

func (c *v2Controller) importCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	if !c.rt.CancelImport() {
		envelopeErr(w, http.StatusConflict, codeConflict, fmt.Errorf("no running import session"))
		return
	}
	envelope(w, http.StatusAccepted, 0, c.rt.ImportStatus(), "")
}

func decodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(v)
}
