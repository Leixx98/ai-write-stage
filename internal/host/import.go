package host

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/agents"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host/imp"
	modelreg "github.com/voocel/ainovel-cli/internal/models"
	"github.com/voocel/ainovel-cli/internal/tools"
)

// ImportFrom 启动一次外部小说语义编译导入：ingest → segment → analyze → synthesize → publish。
// 模型只裁定开放语义（边界/事实/综合），Go 掌管坐标/覆盖/幂等；与 Engine 运行互斥，
// 导入完成后由 AdvanceHold 决定是否续写。
// 返回的事件通道由 imp.Run 关闭，调用方负责消费（满则丢弃以防阻塞管线协程）。
func (h *Host) ImportFrom(ctx context.Context, opts imp.Options) (<-chan imp.Event, error) {
	// 预算启动前置检查与 Start/Resume/Continue 同一纪律：导入是全流程模型调用，
	// 预算已超时不得启动（§13.1「纳入现有预算哨兵」）。
	if err := h.budget.Refuse(); err != nil {
		return nil, err
	}
	if err := h.playActiveError(); err != nil {
		return nil, err
	}
	if err := h.acquireExclusive("导入"); err != nil {
		return nil, err
	}
	// 登记取消函数：预算硬停/手动暂停经 abortWithEvent 取消导入自己的 context
	//（否则哨兵只会去暂停并未运行的 Engine，导入继续烧钱）。
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()

	deps := imp.Deps{
		Store:         h.store,
		CommitChapter: tools.NewCommitChapterTool(h.store, h.styleStats),
		Segment:       h.importCaller("segment"),
		Analyze:       h.importCaller("analyze"),
		Synthesize:    h.importCaller("synthesize"),
		Prompts: imp.Prompts{
			Segment:    h.bundle.Prompts.ImportSegment,
			Analyze:    h.bundle.Prompts.ImportAnalyze,
			Synthesize: h.bundle.Prompts.ImportSynthesize,
			Range:      h.bundle.Prompts.ImportRange,
		},
	}
	ch, err := imp.Run(ctx, deps, opts)
	if err != nil {
		h.releaseExclusive()
		return nil, err
	}
	return h.superviseImport(ch, opts), nil
}

// StartImport is the Host-owned async entry used by web clients. It drains
// the import stream and republishes progress through the normal Host events.
func (h *Host) StartImport(opts imp.Options) error {
	ctx, cancel := context.WithCancel(context.Background())
	session := h.importSessionManager()
	if err := session.begin(opts, cancel); err != nil {
		cancel()
		return err
	}
	ch, err := h.ImportFrom(ctx, opts)
	if err != nil {
		cancel()
		session.fail(err)
		return err
	}
	if !h.launchAsync(func() {
		for ev := range ch {
			session.appendEvent(ev)
			summary := ev.Message
			level := ev.Level
			if ev.Err != nil {
				if level == "" {
					level = "error"
				}
				summary = fmt.Sprintf("%s: %v", summary, ev.Err)
			}
			h.emitEvent(Event{Time: ev.Time, Category: "IMPORT", Summary: summary, Level: level})
		}
	}) {
		cancel()
		err := fmt.Errorf("Host is closing; cannot start import")
		session.fail(err)
		return err
	}
	return nil
}

// ImportStatus returns the UI-neutral import session projection.
func (h *Host) ImportStatus() ImportSessionStatus {
	session := h.importSessionManager()
	status := session.snapshot()
	if status.State != ImportSessionIdle {
		if (status.State == ImportSessionFailed || status.State == ImportSessionCancelled) && !imp.OpenWorkspace(h.store.Dir()).Active() {
			status.CanResume = false
		}
		return status
	}
	workspace := imp.OpenWorkspace(h.store.Dir())
	if !workspace.Active() {
		return status
	}
	facts, err := imp.CollectFacts(h.store, workspace)
	if err != nil {
		return session.restore(ImportSessionFailed, "Import state cannot be read: "+err.Error(), "")
	}
	action := imp.NextAction(facts)
	switch action {
	case imp.ActionDone:
		message := "Import completed"
		if intent, intentErr := workspace.LoadIntent(); intentErr == nil && intent.ContinueAfterImport {
			message = "Import completed; continue writing from the workbench"
		}
		return session.restore(ImportSessionCompleted, message, "")
	case imp.ActionAwaitConfirmation:
		preview, previewErr := imp.PendingConfirmationPreview(h.store)
		if previewErr != nil {
			return session.restore(ImportSessionFailed, "Segmentation preview cannot be read: "+previewErr.Error(), "")
		}
		return session.restore(ImportSessionAwaitingConfirmation, fmt.Sprintf("Segmentation is ready with %d chapters and requires confirmation", facts.ExpectedChapters), preview)
	case imp.ActionAwaitStoryResolution:
		return session.restore(ImportSessionAwaitingStoryStatus, "Story status requires an open or closed decision", "")
	default:
		return session.restore(ImportSessionPaused, fmt.Sprintf("Import can resume from %s", action), "")
	}
}

// ConfirmImport accepts the current segmentation after user review.
func (h *Host) ConfirmImport() error {
	status := h.ImportStatus()
	if status.State != ImportSessionAwaitingConfirmation {
		return fmt.Errorf("import is not awaiting segmentation confirmation")
	}
	return h.StartImport(imp.Options{AcceptSegmentation: true, ContinueAfter: status.ContinueAfter})
}

// ResegmentImport applies replacement guidance and rebuilds segmentation.
func (h *Host) ResegmentImport(guidance string) error {
	status := h.ImportStatus()
	if status.State != ImportSessionAwaitingConfirmation {
		return fmt.Errorf("import is not awaiting segmentation confirmation")
	}
	trimmed := strings.TrimSpace(guidance)
	return h.StartImport(imp.Options{Guidance: trimmed, ResetGuidance: trimmed == "", ContinueAfter: status.ContinueAfter})
}

// ResolveImportStory records the open or closed decision and resumes import.
func (h *Host) ResolveImportStory(storyStatus string) error {
	status := h.ImportStatus()
	if status.State != ImportSessionAwaitingStoryStatus {
		return fmt.Errorf("import is not awaiting story status")
	}
	choice := strings.ToLower(strings.TrimSpace(storyStatus))
	if choice != "open" && choice != "closed" {
		return fmt.Errorf("story status must be open or closed")
	}
	return h.StartImport(imp.Options{StoryResolution: choice, ContinueAfter: status.ContinueAfter})
}

// CancelImport cancels only the active import session.
func (h *Host) CancelImport() bool {
	return h.importSessionManager().cancelRun()
}

func (h *Host) importSessionManager() *importSessionManager {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.importSession == nil {
		h.importSession = newImportSessionManager()
	}
	return h.importSession
}

// ImportResumeHint returns a one-line startup notice for an unfinished import.
// Call it once at startup because it recomputes workspace artifact input digests.
func (h *Host) ImportResumeHint() string {
	status := h.ImportStatus()
	if status.State == ImportSessionIdle || status.State == ImportSessionCompleted {
		return ""
	}
	if status.Message != "" {
		return status.Message
	}
	return "An unfinished import can be resumed in the import panel"
}

// importCaller 解析一个导入语义函数的模型档位（RFC §13.1）：roles 配置存在 import_<fn>
// 则用该档位（用量也记该角色的账），否则落 architect。这是调用配置，不改任何语义契约。
func (h *Host) importCaller(fn string) imp.Caller {
	role := "import_" + fn
	if _, _, explicit := h.models.CurrentSelection(role); !explicit {
		role = "architect"
	}
	model := h.models.ForRoleWithFailover(role, func(ev bootstrap.FailoverEvent) {
		slog.Warn("导入 provider 切换", "module", "import", "role", ev.Role,
			"reason", ev.Reason,
			"from", fmt.Sprintf("%s/%s", ev.FromProvider, ev.FromModel),
			"to", fmt.Sprintf("%s/%s", ev.ToProvider, ev.ToModel),
			"err", ev.Err)
	})
	model = newUsageTrackedModel(model, role, h.usage.Record)
	return imp.Caller{Model: model, Runtime: h.importModelRuntime(role, model)}
}

// importModelRuntime 探测所选档位角色模型的调用能力，供 imp 双预算 / thinking 自适应使用（RFC §13/§21）。
// 探测失败的字段留零值，imp 侧回退保守默认，保证无能力信息也能正确运行。
// 结构化输出由 imp 的 llmcontract 在每次请求前现读模型事实，不在 Runtime 重复缓存。
func (h *Host) importModelRuntime(role string, model agentcore.ChatModel) imp.ModelRuntime {
	var rt imp.ModelRuntime
	provider, name, _ := h.models.CurrentSelection(role)
	if name == "" {
		name = bootstrap.ModelName(model)
		provider = bootstrap.ModelProvider(model)
	}
	// context / completion 上限：registry 是唯一可信来源（被包装模型的 Info() 不含窗口）。
	rt.ContextTokens, _ = h.cfg.ResolveContextWindow(provider, name)
	if entry, ok := modelreg.DefaultRegistry().Resolve(name); ok {
		rt.MaxOutputTokens = entry.MaxTokens
	}
	// thinking：按角色 reasoning effort 与模型能力 resolve；不支持则不发（与 arbiter 同策略）。
	if level, err := agents.ParseThinkingLevel(h.cfg.ResolveReasoningEffort(role)); err == nil {
		if resolved, ok := agents.ResolveThinkingForModel(model, level); ok {
			rt.Thinking = resolved
		}
	}
	return rt
}

// superviseImport exclusively owns post-import continuation. It forwards events,
// releases the exclusive slot before continuing, and records the actual outcome
// in StageDone.Continued so clients do not infer runtime state from local options.
func (h *Host) superviseImport(src <-chan imp.Event, opts imp.Options) <-chan imp.Event {
	out := make(chan imp.Event, 32)
	if !h.launchAsync(func() {
		defer close(out)
		released := false
		release := func() {
			if !released {
				released = true
				h.releaseExclusive()
			}
		}
		defer release()
		for ev := range src {
			if ev.Stage == imp.StageDone {
				release() // 先释放独占槽，接力的 startEngine 才能通过独占门禁
				ev.Continued = h.continueAfterImport(opts)
			} else if ev.RequiresAction || ev.Stage == imp.StageError {
				release()
			}
			select {
			case out <- ev:
			case <-h.runCtx.Done():
				for range src {
				}
				return
			}
		}
	}) {
		close(out)
		h.releaseExclusive()
	}
	return out
}

// continueAfterImport applies a one-shot continuation request and reports whether the Engine started.
func (h *Host) continueAfterImport(opts imp.Options) bool {
	workspace := imp.OpenWorkspace(h.store.Dir())
	intent, err := workspace.LoadIntent()
	if err != nil {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "导入已完成，但自动接力意图读取失败：" + err.Error()})
		return false
	}
	want := opts.ContinueAfter || intent.ContinueAfterImport
	if !want {
		return false
	}
	meta, err := h.store.RunMeta.Load()
	if err != nil || meta == nil {
		slog.Warn("导入自动接力读取 RunMeta 失败", "module", "host", "err", err)
		return false
	}
	if meta.AdvanceMode != domain.ChapterAdvanceAuto {
		if err := h.store.RunMeta.SetAdvanceMode(domain.ChapterAdvanceAuto); err != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
				Summary: "导入已完成，但无法迁移旧的逐章验收状态：" + err.Error()})
			return false
		}
	}
	if _, err := workspace.ConsumeContinueAfterImport(); err != nil {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "导入已完成，但自动接力意图消费失败：" + err.Error()})
		return false
	}
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: "导入完成，自动接力续写"})
	if !h.startEngine(nil) {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "自动接力启动失败，请输入继续指令手动恢复"})
		return false
	}
	return true
}

func (h *Host) recoverCompletedImportContinuation() {
	workspace := imp.OpenWorkspace(h.store.Dir())
	if !workspace.Active() {
		return
	}
	facts, err := imp.CollectFacts(h.store, workspace)
	if err != nil || imp.NextAction(facts) != imp.ActionDone {
		return
	}
	h.continueAfterImport(imp.Options{})
}
