package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/galgame/play"
	"github.com/voocel/ainovel-cli/internal/imagejob"
	imagesvc "github.com/voocel/ainovel-cli/internal/imagejob/service"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

func (h *Host) playActiveError() error {
	return h.playActiveErrorExcept("")
}

func (h *Host) playActiveErrorExcept(id string) error {
	if h == nil || h.roots == nil || h.roots.Tavern == nil {
		return nil
	}
	item, ok, err := h.roots.Tavern.ActivePlay()
	if err != nil {
		return fmt.Errorf("读取剧场状态失败: %w", err)
	}
	if !ok {
		return nil
	}
	if id != "" && item.ID == id {
		return nil
	}
	return fmt.Errorf("剧场进行中，请先在酒馆暂停")
}

func (h *Host) CreatePlay(meta storepkg.PlayMeta) (storepkg.PlayMeta, error) {
	if h == nil || h.roots == nil || h.roots.Tavern == nil {
		return storepkg.PlayMeta{}, fmt.Errorf("tavern store is unavailable")
	}
	if strings.TrimSpace(meta.CharacterID) == "" {
		return storepkg.PlayMeta{}, fmt.Errorf("character_id is required")
	}
	character, err := h.roots.Tavern.LoadCharacter(meta.CharacterID)
	if err != nil {
		return storepkg.PlayMeta{}, err
	}
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(meta.Name) == "" {
		meta.Name = character.Name + " 剧场"
	}
	if meta.ID == "" {
		meta.ID = h.roots.Tavern.NewPlayID(character.Name, meta.Name, meta.CreatedAt)
	}
	meta.Status = storepkg.PlayIdle
	if err := h.roots.Tavern.SavePlay(meta); err != nil {
		return storepkg.PlayMeta{}, err
	}
	if err := h.roots.Tavern.SaveProgress(meta.ID, storepkg.PlayProgress{}); err != nil {
		return storepkg.PlayMeta{}, err
	}
	return meta, nil
}

func (h *Host) ListPlays() ([]storepkg.PlayMeta, error) {
	if h == nil || h.roots == nil || h.roots.Tavern == nil {
		return nil, fmt.Errorf("tavern store is unavailable")
	}
	return h.roots.Tavern.ListPlays()
}

func (h *Host) PlayView(id string) (play.View, error) {
	if h == nil || h.roots == nil || h.roots.Tavern == nil {
		return play.View{}, fmt.Errorf("tavern store is unavailable")
	}
	meta, err := h.roots.Tavern.LoadPlay(id)
	if err != nil {
		return play.View{}, err
	}
	progress, err := h.roots.Tavern.LoadProgress(id)
	if err != nil {
		return play.View{}, err
	}
	beats, err := h.roots.Tavern.ListBeats(id)
	if err != nil {
		return play.View{}, err
	}
	view := play.BuildView(meta, progress, beats)
	if view.Image.JobID != "" && h.roots.Media != nil {
		job, jobErr := h.roots.Media.LoadJob(view.Image.JobID)
		url := ""
		status := ""
		if jobErr == nil {
			status = job.Status
			if status == play.ImageCompleted {
				url = fmt.Sprintf("/api/v2/comfyui/jobs/%s/outputs/0", job.JobID)
			}
		}
		view.ApplyImageJob(status, url, jobErr)
	} else {
		view.ApplyImageJob("", "", nil)
	}
	view.Buffer.ImagesPending = play.CountUnfinishedImages(beats, progress.WriteHead, func(jobID string) bool {
		if h.roots.Media == nil {
			return false
		}
		job, err := h.roots.Media.LoadJob(jobID)
		if err != nil {
			return false
		}
		switch job.Status {
		case play.ImageCompleted, play.ImageFailed, "timeout", "cancelled":
			return true
		default:
			return false
		}
	})
	return view, nil
}

func (h *Host) ListPlayBeats(id string, from int) ([]storepkg.PlayBeat, error) {
	if h == nil || h.roots == nil || h.roots.Tavern == nil {
		return nil, fmt.Errorf("tavern store is unavailable")
	}
	return h.roots.Tavern.ListBeatsFrom(id, from)
}

func (h *Host) StartPlay(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("play id is required")
	}
	if err := h.playActiveErrorExcept(id); err != nil {
		return err
	}
	h.mu.Lock()
	if h.playEngine != nil && h.playEngine.PlayID() == id {
		h.mu.Unlock()
		return nil
	}
	h.mu.Unlock()
	if err := h.acquireExclusive("剧场"); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(h.runCtx)
	engine := h.newPlayEngine(id)
	done := make(chan struct{})
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.playEngine = engine
	h.playDone = done
	h.mu.Unlock()
	if !h.launchAsync(func() {
		defer close(done)
		defer h.finishPlay(id)
		if err := engine.Run(ctx); err != nil && ctx.Err() == nil {
			h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "剧场写作失败", Detail: err.Error(), Level: "error", Kind: "play"})
		}
	}) {
		close(done)
		h.finishPlay(id)
		return fmt.Errorf("Host 正在关闭，不能启动剧场")
	}
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "剧场写作已开始", Level: "info"})
	return nil
}

func (h *Host) PausePlay() error {
	h.mu.Lock()
	if h.exclusive != "剧场" {
		done := h.playDone
		h.mu.Unlock()
		if done != nil {
			<-done
		}
		return nil
	}
	cancel := h.exclusiveCancel
	done := h.playDone
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	return nil
}

func (h *Host) finishPlay(id string) {
	h.mu.Lock()
	if h.playEngine != nil && h.playEngine.PlayID() == id {
		h.playEngine = nil
	}
	h.mu.Unlock()
	h.releaseExclusive()
}

func (h *Host) AdvancePlay(id string) (storepkg.PlayProgress, error) {
	engine := h.playHandle(id)
	progress, err := engine.Advance()
	if err != nil {
		return progress, err
	}
	return progress, nil
}

func (h *Host) ChoosePlay(id, choiceID string) (storepkg.PlayProgress, error) {
	h.mu.Lock()
	running := h.playEngine
	h.mu.Unlock()
	engine := running
	if engine == nil || engine.PlayID() != id {
		engine = play.New(play.Config{Store: h.roots.Tavern, PlayID: id})
	}
	progress, err := engine.Choose(choiceID)
	if err != nil {
		return progress, err
	}
	if running == nil || running.PlayID() != id {
		if startErr := h.StartPlay(id); startErr != nil {
			return progress, startErr
		}
	}
	return progress, nil
}

func (h *Host) playHandle(id string) *play.Engine {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.playEngine != nil && h.playEngine.PlayID() == id {
		return h.playEngine
	}
	return play.New(play.Config{Store: h.roots.Tavern, PlayID: id})
}

func (h *Host) newPlayEngine(id string) *play.Engine {
	cfg := play.Config{Store: h.roots.Tavern, PlayID: id, TextAhead: play.DefaultTextAhead, StartImage: h.startPlayImage}
	if h.playArchitect != nil && h.playPlanner != nil && h.playWriter != nil {
		cfg.Architect, cfg.Planner, cfg.Writer = h.playArchitect, h.playPlanner, h.playWriter
		return play.New(cfg)
	}
	record := h.usage.Record
	gen := play.Generator{
		ArchitectModel:  newUsageTrackedModel(h.models.ForRole("architect"), "galplay", record),
		PlannerModel:    newUsageTrackedModel(h.models.ForRole("chapter_planner"), "galplay", record),
		WriterModel:     newUsageTrackedModel(h.models.ForRole("writer"), "galplay", record),
		ArchitectPrompt: h.bundle.Prompts.PlayArchitect,
		PlannerPrompt:   h.bundle.Prompts.PlayPlanner,
		WriterPrompt:    h.bundle.Prompts.PlayWriter,
	}
	cfg.Architect, cfg.Planner, cfg.Writer = gen.Architect, gen.Planner, gen.Writer
	return play.New(cfg)
}

func (h *Host) startPlayImage(_ context.Context, playID string, beat *storepkg.PlayBeat) error {
	if beat == nil || beat.CG != storepkg.PlayCGNew || h.roots == nil || h.roots.Media == nil {
		return nil
	}
	bridge, err := h.roots.Media.LoadBridgeConfig()
	if err != nil {
		return err
	}
	if !bridge.Enabled {
		return nil
	}
	meta, err := h.roots.Tavern.LoadPlay(playID)
	if err != nil {
		return err
	}
	character, err := h.roots.Tavern.LoadCharacter(meta.CharacterID)
	if err != nil {
		return err
	}
	workflowID := strings.TrimSpace(meta.ImageWorkflowID)
	if workflowID == "" {
		workflowID = bridge.WorkflowID
	}
	workflow, err := h.roots.Media.LoadWorkflow(workflowID)
	if err != nil {
		return err
	}
	canvas, err := h.roots.Media.LoadOrCreateWorkflowCanvas(workflow.ID)
	if err != nil {
		return err
	}
	promptSchema, err := imagejob.BuildPromptSchema(workflow.ID, canvas)
	if err != nil {
		return err
	}
	if len(promptSchema.Fields) == 0 {
		return fmt.Errorf("play image schema is empty")
	}
	schemaJSON, err := json.Marshal(promptSchema.Schema)
	if err != nil {
		return err
	}
	svc := h.ensureImageService()
	run := imagesvc.PromptRun{
		Request: imagejob.PromptRequest{
			UnitID: fmt.Sprintf("%s/%d", meta.ID, beat.Ordinal), ChapterTitle: character.Name,
			UnitPlan: strings.TrimSpace(strings.Join([]string{beat.Location, beat.TimeOfDay, beat.CGIntent, character.Description}, "\n")),
			UnitText: beat.Text, Schema: schemaJSON, SchemaHash: promptSchema.SchemaHash, SystemPrompt: promptSchema.Composed,
		},
		Workflow: workflow, Canvas: canvas, Bridge: bridge, Schema: promptSchema,
	}
	job, err := svc.StartPlayBeat(meta.ID, beat.Ordinal, run)
	if err != nil {
		return err
	}
	beat.ImageJobID = job.JobID
	return nil
}

func (h *Host) ensureImageService() *imagesvc.Service {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.imageSvc == nil {
		h.imageSvc = imagesvc.New(imagesvc.Config{
			Root:     h.Dir(),
			Store:    h.roots.Media,
			Prompter: imagejob.PrompterFunc(h.GenerateImagePrompt),
		})
	}
	return h.imageSvc
}
