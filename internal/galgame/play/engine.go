package play

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/voocel/ainovel-cli/internal/galgame/runlog"
	"github.com/voocel/ainovel-cli/internal/store"
)

const DefaultTextAhead = 8

const (
	StagePlanning   = "planning"
	StageStoryboard = "storyboard"
	StageWriting    = "writing"
)

type ArchitectFunc func(context.Context, ArchitectInput) (ArchitectOutput, error)
type PlannerFunc func(context.Context, PlannerInput) (PlannerOutput, error)
type WriterFunc func(context.Context, WriterInput) (WriterOutput, error)
type ImageStartFunc func(context.Context, string, *store.PlayBeat) error

type Config struct {
	Store      *store.GalgameStore
	PlayID     string
	TextAhead  int
	Architect  ArchitectFunc
	Planner    PlannerFunc
	Writer     WriterFunc
	StartImage ImageStartFunc
}

type Engine struct {
	store      *store.GalgameStore
	playID     string
	textAhead  int
	architect  ArchitectFunc
	planner    PlannerFunc
	writer     WriterFunc
	startImage ImageStartFunc
	wake       chan struct{}
	mu         sync.Mutex
}

func New(cfg Config) *Engine {
	ahead := cfg.TextAhead
	if ahead <= 0 {
		ahead = DefaultTextAhead
	}
	return &Engine{
		store:      cfg.Store,
		playID:     cfg.PlayID,
		textAhead:  ahead,
		architect:  cfg.Architect,
		planner:    cfg.Planner,
		writer:     cfg.Writer,
		startImage: cfg.StartImage,
		wake:       make(chan struct{}, 1),
	}
}

func (e *Engine) PlayID() string { return e.playID }

func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) Run(ctx context.Context) error {
	if e == nil || e.store == nil || strings.TrimSpace(e.playID) == "" {
		return fmt.Errorf("play engine is not configured")
	}
	defer e.persistPaused()
	if err := e.markStarted(); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		progress, err := e.store.LoadProgress(e.playID)
		if err != nil {
			return e.fail(err)
		}
		meta, err := e.store.LoadPlay(e.playID)
		if err != nil {
			return e.fail(err)
		}
		if meta.Status == store.PlayCompleted {
			return nil
		}
		if progress.GateOrdinal != 0 {
			e.setStage("")
			e.note(fmt.Sprintf("等待玩家选项 gate=%d play_head=%d write_head=%d", progress.GateOrdinal, progress.PlayHead, progress.WriteHead))
			if waitErr := e.wait(ctx); waitErr != nil {
				return waitErr
			}
			continue
		}
		if progress.WriteHead-progress.PlayHead >= e.textAhead {
			e.setStage("")
			e.note(fmt.Sprintf("缓冲已满，等待翻页 play_head=%d write_head=%d ahead=%d", progress.PlayHead, progress.WriteHead, e.textAhead))
			if waitErr := e.wait(ctx); waitErr != nil {
				return waitErr
			}
			continue
		}
		outline, err := e.store.LoadOutline(e.playID)
		if err != nil {
			return e.fail(err)
		}
		if outline.NextCard >= len(outline.Cards) {
			if outline.CompleteAfterSegment && progress.WriteHead > 0 && outline.SegmentID != "" {
				return e.complete()
			}
			if err := e.planNextSegment(ctx, progress); err != nil {
				return e.fail(err)
			}
			continue
		}
		if err := e.writeNextBeat(ctx, progress, outline); err != nil {
			return e.fail(err)
		}
	}
}

func (e *Engine) markStarted() error {
	meta, err := e.store.LoadPlay(e.playID)
	if err != nil {
		return err
	}
	progress, err := e.store.LoadProgress(e.playID)
	if err != nil {
		return err
	}
	if progress.GateOrdinal != 0 {
		meta.Status = store.PlayAwaitingChoice
	} else {
		meta.Status = store.PlayRunning
	}
	meta.LastError = ""
	if err := e.store.SavePlay(meta); err != nil {
		return err
	}
	e.note(fmt.Sprintf("剧场引擎启动 status=%s play_head=%d write_head=%d gate=%d", meta.Status, progress.PlayHead, progress.WriteHead, progress.GateOrdinal))
	return nil
}

func (e *Engine) persistPaused() {
	meta, err := e.store.LoadPlay(e.playID)
	if err != nil || meta.Status == store.PlayCompleted {
		return
	}
	if meta.Status == store.PlayPaused {
		return
	}
	meta.Status = store.PlayPaused
	meta.Stage = ""
	_ = e.store.SavePlay(meta)
	e.note("剧场引擎已暂停")
}

func (e *Engine) fail(err error) error {
	meta, loadErr := e.store.LoadPlay(e.playID)
	if loadErr == nil {
		meta.Status = store.PlayPaused
		meta.LastError = err.Error()
		meta.Stage = ""
		_ = e.store.SavePlay(meta)
	}
	e.note("剧场失败: " + err.Error())
	return err
}

func (e *Engine) complete() error {
	meta, err := e.store.LoadPlay(e.playID)
	if err != nil {
		return err
	}
	meta.Status = store.PlayCompleted
	meta.LastError = ""
	meta.Stage = ""
	if err := e.store.SavePlay(meta); err != nil {
		return err
	}
	e.note("剧场完成")
	return nil
}

func (e *Engine) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.wake:
		return nil
	}
}

func (e *Engine) planNextSegment(ctx context.Context, progress store.PlayProgress) error {
	if e.architect == nil || e.planner == nil {
		return fmt.Errorf("play planner is unavailable")
	}
	meta, err := e.store.LoadPlay(e.playID)
	if err != nil {
		return err
	}
	character, err := e.store.LoadCharacter(meta.CharacterID)
	if err != nil {
		return err
	}
	beats, err := e.store.ListBeats(e.playID)
	if err != nil {
		return err
	}
	recent := tailBeats(beats, 6)
	lastGoal := ""
	if prev, loadErr := e.store.LoadOutline(e.playID); loadErr == nil {
		lastGoal = prev.Goal
	}
	e.setStage(StagePlanning)
	e.note(fmt.Sprintf("开始规划下一段 last_goal=%s choices=%d", lastGoal, len(progress.ChoiceHistory)))
	arch, err := e.architect(ctx, ArchitectInput{
		Character: character, Premise: meta.Premise, UserPersona: meta.UserPersona,
		ChoiceHistory: progress.ChoiceHistory, RecentBeats: recent, LastGoal: lastGoal,
	})
	if err != nil {
		return err
	}
	e.setStage(StageStoryboard)
	location := ""
	if len(recent) > 0 {
		location = recent[len(recent)-1].Location
	}
	plan, err := e.planner(ctx, PlannerInput{Architect: arch, Character: character, Premise: meta.Premise, UserPersona: meta.UserPersona, Location: location})
	if err != nil {
		return err
	}
	if err := validatePlannerAgainstArchitect(plan, arch); err != nil {
		return err
	}
	outline := store.PlayOutline{
		SegmentID: arch.SegmentID, Goal: arch.Goal, CompleteAfterSegment: arch.CompleteAfterSegment,
		Notes: arch.Notes, Cards: plan.Cards, NextCard: 0,
	}
	if err := e.store.SaveOutline(e.playID, outline); err != nil {
		return err
	}
	progress.SegmentID = arch.SegmentID
	if err := e.store.SaveProgress(e.playID, progress); err != nil {
		return err
	}
	e.note(fmt.Sprintf("规划完成 segment=%s cards=%d complete_after=%t", arch.SegmentID, len(plan.Cards), arch.CompleteAfterSegment))
	return nil
}

func (e *Engine) writeNextBeat(ctx context.Context, progress store.PlayProgress, outline store.PlayOutline) error {
	if e.writer == nil {
		return fmt.Errorf("play writer is unavailable")
	}
	card := outline.Cards[outline.NextCard]
	meta, err := e.store.LoadPlay(e.playID)
	if err != nil {
		return err
	}
	character, err := e.store.LoadCharacter(meta.CharacterID)
	if err != nil {
		return err
	}
	e.setStage(StageWriting)
	meta.Stage = StageWriting
	e.note(fmt.Sprintf("开始写拍 card=%d kind=%s cg=%s location=%s", outline.NextCard, card.Kind, card.CG, card.Location))
	beat := store.PlayBeat{
		Ordinal: progress.WriteHead + 1, SegmentID: outline.SegmentID, Kind: card.Kind,
		Speaker: card.Speaker, Location: card.Location, TimeOfDay: card.TimeOfDay,
		CG: card.CG, CGIntent: card.CGIntent, Choices: card.Choices,
		Text: strings.Join(card.RequiredBeats, "\n"),
	}
	imageDeferred := false
	if beat.CG == store.PlayCGNew && e.startImage != nil {
		if err := e.startImage(ctx, e.playID, &beat); err != nil {
			beat.ImageError = err.Error()
			e.note(fmt.Sprintf("配图启动失败 ordinal=%d err=%s", beat.Ordinal, err.Error()))
		} else {
			imageDeferred = beat.ImageJobID == ""
		}
	}
	written, err := e.writer(ctx, WriterInput{
		Card: card, Character: character, Premise: meta.Premise, UserPersona: meta.UserPersona,
	})
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	progress, err = e.store.LoadProgress(e.playID)
	if err != nil {
		return err
	}
	outline, err = e.store.LoadOutline(e.playID)
	if err != nil {
		return err
	}
	if outline.NextCard >= len(outline.Cards) {
		return nil
	}
	card = outline.Cards[outline.NextCard]
	beat.Kind = card.Kind
	beat.Location = card.Location
	beat.TimeOfDay = card.TimeOfDay
	beat.CG = card.CG
	beat.CGIntent = card.CGIntent
	beat.Choices = card.Choices
	beat.Speaker = strings.TrimSpace(written.Speaker)
	if beat.Speaker == "" {
		beat.Speaker = card.Speaker
	}
	beat.Text = strings.TrimSpace(written.Text)
	// A scene switch can be enabled while Writer is producing this beat. Retry
	// only a clean skip; an existing job or a real startup error is never repeated.
	if imageDeferred && beat.CG == store.PlayCGNew && e.startImage != nil {
		if err := e.startImage(ctx, e.playID, &beat); err != nil {
			beat.ImageError = err.Error()
			e.note(fmt.Sprintf("配图二次检查失败 ordinal=%d err=%s", beat.Ordinal, err.Error()))
		}
	}
	if err := e.store.SaveBeat(e.playID, beat); err != nil {
		return err
	}
	outline.NextCard++
	if err := e.store.SaveOutline(e.playID, outline); err != nil {
		return err
	}
	progress.WriteHead = beat.Ordinal
	if progress.PlayHead == 0 {
		progress.PlayHead = beat.Ordinal
	}
	if beat.Kind == store.BeatChoice {
		progress.GateOrdinal = beat.Ordinal
		if err := e.store.SaveProgress(e.playID, progress); err != nil {
			return err
		}
		meta.Status = store.PlayAwaitingChoice
		meta.Stage = ""
		if err := e.store.SavePlay(meta); err != nil {
			return err
		}
		e.note(fmt.Sprintf("已写拍 ordinal=%d kind=%s cg=%s 进入选项", beat.Ordinal, beat.Kind, beat.CG))
		return nil
	}
	if err := e.store.SaveProgress(e.playID, progress); err != nil {
		return err
	}
	e.note(fmt.Sprintf("已写拍 ordinal=%d kind=%s cg=%s", beat.Ordinal, beat.Kind, beat.CG))
	if outline.NextCard >= len(outline.Cards) && outline.CompleteAfterSegment {
		return e.complete()
	}
	return nil
}

func choiceResolved(progress store.PlayProgress, ordinal int) bool {
	for _, item := range progress.ChoiceHistory {
		if item.Ordinal == ordinal {
			return true
		}
	}
	return false
}

func tailBeats(beats []store.PlayBeat, n int) []store.PlayBeat {
	if n <= 0 || len(beats) <= n {
		return beats
	}
	return beats[len(beats)-n:]
}

func (e *Engine) Advance() (store.PlayProgress, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	progress, err := e.store.LoadProgress(e.playID)
	if err != nil {
		return progress, err
	}
	if progress.PlayHead >= progress.WriteHead {
		return progress, fmt.Errorf("next beat is not ready")
	}
	current, err := e.store.LoadBeat(e.playID, progress.PlayHead)
	if err != nil {
		return progress, err
	}
	if current.Kind == store.BeatChoice && !choiceResolved(progress, current.Ordinal) {
		return progress, fmt.Errorf("choice beat cannot be advanced")
	}
	progress.PlayHead++
	if err := e.store.SaveProgress(e.playID, progress); err != nil {
		return progress, err
	}
	e.Wake()
	e.note(fmt.Sprintf("翻页 play_head=%d write_head=%d", progress.PlayHead, progress.WriteHead))
	return progress, nil
}

func (e *Engine) Choose(choiceID string) (store.PlayProgress, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	choiceID = strings.TrimSpace(choiceID)
	if choiceID == "" {
		return store.PlayProgress{}, fmt.Errorf("choice_id is required")
	}
	progress, err := e.store.LoadProgress(e.playID)
	if err != nil {
		return progress, err
	}
	if progress.GateOrdinal == 0 {
		return progress, fmt.Errorf("play is not awaiting a choice")
	}
	beat, err := e.store.LoadBeat(e.playID, progress.GateOrdinal)
	if err != nil {
		return progress, err
	}
	var selected *store.PlayChoice
	for i := range beat.Choices {
		if beat.Choices[i].ID == choiceID {
			selected = &beat.Choices[i]
			break
		}
	}
	if selected == nil {
		return progress, fmt.Errorf("unknown choice_id %q", choiceID)
	}
	progress.ChoiceHistory = append(progress.ChoiceHistory, store.PlayChoiceRecord{
		Ordinal: beat.Ordinal, ChoiceID: selected.ID, Label: selected.Label,
	})
	progress.GateOrdinal = 0
	progress.SegmentID = ""
	if progress.PlayHead < beat.Ordinal {
		progress.PlayHead = beat.Ordinal
	}
	if err := e.store.SaveProgress(e.playID, progress); err != nil {
		return progress, err
	}
	if err := e.store.SaveOutline(e.playID, store.PlayOutline{}); err != nil {
		return progress, err
	}
	meta, err := e.store.LoadPlay(e.playID)
	if err != nil {
		return progress, err
	}
	if meta.Status == store.PlayAwaitingChoice {
		meta.Status = store.PlayRunning
		meta.LastError = ""
		if err := e.store.SavePlay(meta); err != nil {
			return progress, err
		}
	}
	e.Wake()
	e.note(fmt.Sprintf("玩家选择 choice=%s label=%s gate=%d", selected.ID, selected.Label, beat.Ordinal))
	return progress, nil
}

func (e *Engine) note(message string) {
	if e == nil || e.store == nil {
		return
	}
	runlog.Note(e.store, runlog.Record{
		Mode:      runlog.ModePlay,
		Step:      "engine",
		PlayID:    e.playID,
		Streaming: false,
		Message:   message,
	})
}

func (e *Engine) setStage(stage string) {
	if e == nil || e.store == nil {
		return
	}
	meta, err := e.store.LoadPlay(e.playID)
	if err != nil {
		return
	}
	if meta.Stage == stage {
		return
	}
	meta.Stage = stage
	_ = e.store.SavePlay(meta)
}
