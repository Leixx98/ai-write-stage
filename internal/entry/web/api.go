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
	"github.com/voocel/ainovel-cli/internal/imagejob"
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
	rt     *host.Host
	st     *store.Store
	media  *store.ComfyUIStore
	tavern *store.GalgameStore
	svc    *imagesvc.Service
	chat   galgame.GenerateFunc
	mu     sync.Mutex
}

func newV2Controller(rt *host.Host) *v2Controller {
	roots := rt.Roots()
	st := roots.Facts
	c := &v2Controller{rt: rt, st: st, media: roots.Media, tavern: roots.Tavern, chat: rt.NewGalgameGenerate()}
	c.svc = imagesvc.New(imagesvc.Config{
		Root:           rt.Dir(),
		Store:          roots.Media,
		Prompter:       imagejob.PrompterFunc(rt.GenerateImagePrompt),
		LoadUnitPrompt: c.unitPromptRequest,
	})
	go c.watchCompletedUnits()
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
	case p == "comfyui/config":
		c.config(w, r)
	case p == "galgame/characters":
		c.galgameCharacters(w, r)
	case strings.HasPrefix(p, "galgame/characters/"):
		c.galgameCharacter(w, r, strings.TrimPrefix(p, "galgame/characters/"))
	case p == "galgame/sessions":
		c.galgameSessions(w, r)
	case strings.HasPrefix(p, "galgame/sessions/"):
		c.galgameSession(w, r, strings.TrimPrefix(p, "galgame/sessions/"))
	case p == "comfyui/test-connection":
		c.testConnection(w, r)
	case p == "comfyui/bridge":
		c.bridgeConfig(w, r)
	case p == "comfyui/prompter-presets":
		c.prompterPresets(w, r)
	case p == "comfyui/prompter/parse" && r.Method == http.MethodPost:
		c.parsePrompterJSON(w, r)
	case p == "comfyui/instances" && r.Method == http.MethodGet:
		c.instances(w, r)
	case p == "comfyui/instances" && r.Method == http.MethodPut:
		c.saveInstances(w, r)
	case strings.HasPrefix(p, "comfyui/instances/") && strings.HasSuffix(p, "/test"):
		c.testInstance(w, r, strings.TrimSuffix(strings.TrimPrefix(p, "comfyui/instances/"), "/test"))
	case p == "comfyui/media/upload" && r.Method == http.MethodPost:
		c.uploadMedia(w, r)
	case strings.HasPrefix(p, "comfyui/media/"):
		c.getMedia(w, r, strings.TrimPrefix(p, "comfyui/media/"))
	case p == "comfyui/workflows" && r.Method == http.MethodGet:
		c.listWorkflows(w, r)
	case p == "comfyui/workflows/import" && r.Method == http.MethodPost:
		c.importWorkflow(w, r)
	case strings.HasPrefix(p, "comfyui/workflows/"):
		c.workflow(w, r, strings.TrimPrefix(p, "comfyui/workflows/"))
	case p == "comfyui/jobs/test" && r.Method == http.MethodPost:
		c.testJob(w, r)
	case strings.Contains(p, "/outputs/"):
		c.jobOutput(w, r, strings.TrimPrefix(strings.TrimPrefix(p, "comfyui/jobs/"), ""))
	case strings.HasPrefix(p, "comfyui/jobs/"):
		c.job(w, r, strings.TrimPrefix(p, "comfyui/jobs/"))
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
	result, err := exp.Run(r.Context(), exp.Deps{Store: c.st}, exp.Options{Format: exp.FormatEPUB, Overwrite: true})
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
	w.Header().Set("Content-Type", "application/epub+zip")
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
		Source      string `json:"source"`
		Reference   string `json:"reference"`
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
	case "import":
		source := strings.TrimSpace(req.Source)
		if source == "" {
			err = fmt.Errorf("import source is required")
			break
		}
		err = c.rt.StartImport(imp.Options{SourcePath: source, AutoConfirm: true, ContinueAfter: true})
	case "imitate":
		reference := strings.TrimSpace(req.Reference)
		if reference == "" {
			err = fmt.Errorf("imitation reference is required")
			break
		}
		if info, statErr := os.Stat(reference); statErr == nil && !info.IsDir() {
			reference = filepath.Dir(reference)
		}
		err = c.rt.StartSimulation(reference)
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

func decodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(v)
}
