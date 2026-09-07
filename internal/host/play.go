package host

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Leixx98/ai-write-stage/internal/galgame/play"
	"github.com/Leixx98/ai-write-stage/internal/galgame/runlog"
	"github.com/Leixx98/ai-write-stage/internal/imagejob"
	"github.com/Leixx98/ai-write-stage/internal/imagejob/comfyadapter"
	imagesvc "github.com/Leixx98/ai-write-stage/internal/imagejob/service"
	storepkg "github.com/Leixx98/ai-write-stage/internal/store"
	"github.com/voocel/agentcore"
)

func (h *Host) playActiveError() error {
	return h.playActiveErrorExcept("")
}

func (h *Host) playLive() (engineID string, occupied bool) {
	if h == nil {
		return "", false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.playEngine != nil {
		engineID = h.playEngine.PlayID()
	}
	return engineID, h.exclusive == "剧场" || engineID != ""
}

func (h *Host) playActiveErrorExcept(id string) error {
	engineID, occupied := h.playLive()
	if occupied {
		if id != "" && engineID == id {
			return nil
		}
		return fmt.Errorf("剧场进行中，请先在酒馆暂停")
	}
	_ = h.pauseInactivePlays()
	return nil
}

func (h *Host) pauseInactivePlays() error {
	if h == nil || h.roots == nil || h.roots.Tavern == nil {
		return nil
	}
	engineID, occupied := h.playLive()
	plays, err := h.roots.Tavern.ListPlays()
	if err != nil {
		return err
	}
	for _, item := range plays {
		if !item.Status.Active() {
			continue
		}
		if occupied && item.ID == engineID {
			continue
		}
		item.Status = storepkg.PlayPaused
		if err := h.roots.Tavern.SavePlay(item); err != nil {
			return err
		}
	}
	return nil
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
	meta.Density = storepkg.NormalizePlayDensity(string(meta.Density))
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

func (h *Host) UpdatePlay(id string, update storepkg.PlayMeta) (storepkg.PlayMeta, error) {
	if h == nil || h.roots == nil || h.roots.Tavern == nil {
		return storepkg.PlayMeta{}, fmt.Errorf("tavern store is unavailable")
	}
	meta, err := h.roots.Tavern.LoadPlay(id)
	if err != nil {
		return storepkg.PlayMeta{}, err
	}
	if name := strings.TrimSpace(update.Name); name != "" {
		meta.Name = name
	}
	if premise := strings.TrimSpace(update.Premise); premise != "" {
		meta.Premise = premise
	}
	meta.UserPersona = strings.TrimSpace(update.UserPersona)
	meta.ImageProfileID = strings.TrimSpace(update.ImageProfileID)
	if update.Density != "" {
		meta.Density = storepkg.NormalizePlayDensity(string(update.Density))
	}
	if err := h.roots.Tavern.SavePlay(meta); err != nil {
		return storepkg.PlayMeta{}, err
	}
	return meta, nil
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
	if view.Image.JobID != "" && h.roots.Images != nil {
		job, jobErr := h.roots.Images.LoadJob(view.Image.JobID)
		url := ""
		status := ""
		if jobErr == nil {
			status = job.Status
			if status == play.ImageCompleted {
				url = fmt.Sprintf("/api/v2/image-jobs/%s/outputs/0", job.JobID)
			}
		}
		view.ApplyImageJob(status, url, jobErr)
	} else {
		view.ApplyImageJob("", "", nil)
	}
	if h.roots.Images != nil {
		view.Buffer = summarizePlayImageBuffer(view.Buffer, beats, progress.WriteHead, h.roots.Images.LoadJob)
	}
	return view, nil
}

// summarizePlayImageBuffer reports current image activity. Historical failures
// remain in the run log, but only the latest new-image outcome can occupy the
// play status after all active jobs have settled.
func summarizePlayImageBuffer(buffer play.BufferInfo, beats []storepkg.PlayBeat, writeHead int, loadJob func(string) (storepkg.ImageJob, error)) play.BufferInfo {
	buffer.ImageGenerating = false
	buffer.ImageProgress = 0
	buffer.ImageProgressNode = ""
	buffer.ImageError = ""
	latestOutcomeCaptured := false
	for i := len(beats) - 1; i >= 0; i-- {
		beat := beats[i]
		if beat.CG != storepkg.PlayCGNew || beat.Ordinal > writeHead || beat.Ordinal < 1 {
			continue
		}
		jobID := strings.TrimSpace(beat.ImageJobID)
		if jobID == "" {
			if !latestOutcomeCaptured {
				latestOutcomeCaptured = true
				buffer.ImageError = strings.TrimSpace(beat.ImageError)
			}
			continue
		}
		job, err := loadJob(jobID)
		if err != nil {
			if !latestOutcomeCaptured {
				latestOutcomeCaptured = true
			}
			continue
		}
		switch job.Status {
		case "completed":
			if !latestOutcomeCaptured {
				latestOutcomeCaptured = true
			}
		case "failed", "timeout", "cancelled":
			if !latestOutcomeCaptured {
				latestOutcomeCaptured = true
				buffer.ImageError = strings.TrimSpace(job.Error)
			}
		default:
			buffer.ImageGenerating = true
			if job.ProgressTotal > 0 {
				pct := job.ProgressCurrent * 100 / job.ProgressTotal
				if pct < 0 {
					pct = 0
				} else if pct > 100 {
					pct = 100
				}
				buffer.ImageProgress = pct
				buffer.ImageProgressNode = job.ProgressNode
			}
			buffer.ImageError = ""
			return buffer
		}
	}
	return buffer
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
	exclusive := h.exclusive
	cancel := h.exclusiveCancel
	done := h.playDone
	h.mu.Unlock()
	if exclusive == "剧场" && cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	return h.pauseInactivePlays()
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
		cfg.Spine, cfg.Architect, cfg.Planner, cfg.Writer = h.playSpine, h.playArchitect, h.playPlanner, h.playWriter
		return play.New(cfg)
	}
	h.mu.Lock()
	var record func(string, string, agentcore.AgentMessage)
	if h.usage != nil {
		record = h.usage.Record
	}
	archThink := h.resolveThinkingForRoleLocked("architect")
	planThink := h.resolveThinkingForRoleLocked("chapter_planner")
	writeThink := h.resolveThinkingForRoleLocked("writer")
	h.mu.Unlock()
	writerProvider, writerModel, _ := h.models.CurrentSelection("writer")
	writerWindow, _ := h.models.ResolveContextWindow(writerProvider, writerModel)
	density := storepkg.PlayDensityCompact
	if meta, err := h.roots.Tavern.LoadPlay(id); err == nil {
		density = storepkg.NormalizePlayDensity(string(meta.Density))
	}
	gen := play.Generator{
		ArchitectModel:    newUsageTrackedModel(h.models.ForRole("architect"), "galplay", record),
		PlannerModel:      newUsageTrackedModel(h.models.ForRole("chapter_planner"), "galplay", record),
		WriterModel:       newUsageTrackedModel(h.models.ForRole("writer"), "galplay", record),
		SpinePrompt:       h.bundle.Prompts.PlaySpine,
		ArchitectPrompt:   h.bundle.Prompts.PlayArchitect,
		PlannerPrompt:     h.bundle.Prompts.PlayPlanner,
		WriterPrompt:      h.bundle.Prompts.PlayWriter,
		Density:           density,
		ArchitectThinking: archThink,
		PlannerThinking:   planThink,
		WriterThinking:    writeThink,
		PlayID:            id,
		Store:             h.roots.Tavern,
		ContextWindow:     writerWindow,
		Sink:              h.roots.Tavern,
	}
	cfg.Spine, cfg.Architect, cfg.Planner, cfg.Writer = gen.Spine, gen.Architect, gen.Planner, gen.Writer
	return play.New(cfg)
}

func (h *Host) startPlayImage(_ context.Context, playID string, beat *storepkg.PlayBeat) error {
	if beat == nil || beat.CG != storepkg.PlayCGNew || h.roots == nil || h.roots.Images == nil {
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
	svc := h.ensureImageService()
	request := imagejob.SceneImageRequest{
		Scene: imagejob.ScenePlay, SceneID: meta.ID, UnitID: fmt.Sprintf("%s/%d", meta.ID, beat.Ordinal),
		Ordinal: beat.Ordinal, Title: character.Name, Text: beat.Text,
		VisualIntent: strings.TrimSpace(strings.Join([]string{beat.Location, beat.TimeOfDay, beat.CGIntent}, "\n")),
		Characters:   []imagejob.CharacterContext{{Name: character.Name, Description: character.Description}},
		ProfileID:    meta.ImageProfileID,
	}
	job, skipped, err := svc.Start(request)
	if err != nil {
		return err
	}
	if skipped {
		h.playLogNote(playID, "play_image",
			fmt.Sprintf("跳过配图 ordinal=%d status=skipped reason=scene_disabled_or_auto_disabled", beat.Ordinal))
		return nil
	}
	beat.ImageJobID = job.JobID
	h.playLogNote(playID, "play_image",
		fmt.Sprintf("开始配图 ordinal=%d job=%s status=prompting", beat.Ordinal, job.JobID))
	return nil
}

func (h *Host) ensureImageService() *imagesvc.Service {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.imageSvc == nil {
		adapter := comfyadapter.New(h.roots.ComfyUI, h.roots.ImageConfig, imagejob.PrompterFunc(h.GenerateImagePrompt))
		h.imageSvc = imagesvc.New(imagesvc.Config{
			Root:          h.Dir(),
			Jobs:          h.roots.Images,
			Configuration: h.roots.ImageConfig,
			Providers:     imagesvc.NewRegistry(adapter),
			StageObserver: func(job storepkg.ImageJob, prevStatus, prevStage string) {
				h.emitEvent(Event{ID: job.JobID, Time: time.Now(), Category: "image.job." + job.Status, Summary: job.Stage, Detail: job.JobID, Failed: job.Status == "failed" || job.Status == "timeout", Level: "info"})
				msg := fmt.Sprintf("配图进度 job=%s trigger=%s status=%s→%s stage=%s→%s",
					job.JobID, job.Trigger, prevStatus, job.Status, prevStage, job.Stage)
				if job.Error != "" {
					msg += " err=" + job.Error
				}
				if job.PlayID != "" {
					h.playLogNote(job.PlayID, "play_image",
						fmt.Sprintf("配图进度 ordinal=%d %s", job.Ordinal, msg))
				}
			},
		})
	}
	return h.imageSvc
}

func (h *Host) ImageService() *imagesvc.Service { return h.ensureImageService() }

// playLogNote 向剧场的运行时日志写入一条 NOTE 记录。
func (h *Host) playLogNote(playID, step, message string) {
	if h == nil || h.roots == nil || h.roots.Tavern == nil || playID == "" {
		return
	}
	runlog.Note(h.roots.Tavern, runlog.Record{
		Mode:      runlog.ModePlay,
		Step:      step,
		PlayID:    playID,
		Streaming: false,
		Message:   message,
	})
}
