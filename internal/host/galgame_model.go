package host

import (
	"context"
	"fmt"
	"strings"

	"github.com/Leixx98/ai-write-stage/internal/galgame"
	"github.com/Leixx98/ai-write-stage/internal/galgame/runlog"
	"github.com/voocel/agentcore"
)

func (h *Host) NewGalgameGenerate() galgame.StreamFunc {
	return func(ctx context.Context, msgs []agentcore.Message, emit func(galgame.Delta)) (string, error) {
		if h == nil || h.models == nil {
			return "", fmt.Errorf("galgame model is unavailable")
		}
		h.mu.Lock()
		var record func(string, string, agentcore.AgentMessage)
		if h.usage != nil {
			record = h.usage.Record
		}
		model := newUsageTrackedModel(h.models.ForRole("galgame"), "galgame", record)
		thinking := h.resolveThinkingForRoleLocked("galgame")
		provider, modelName, _ := h.models.CurrentSelection("galgame")
		h.mu.Unlock()
		scope := runlog.ChatFrom(ctx)
		var sink runlog.Sink
		if h.roots != nil {
			sink = h.roots.Tavern
		}
		call := runlog.NewCall(runlog.Record{
			Mode:        runlog.ModeChat,
			Step:        "reply",
			SessionID:   scope.SessionID,
			CharacterID: scope.CharacterID,
			Provider:    provider,
			Model:       modelName,
			Thinking:    string(thinking),
			MaxTokens:   galgame.MaxReplyTokens(),
			PromptChars: runlog.PromptChars(msgs),
			Streaming:   true,
		})
		runlog.Start(sink, call)
		stopHB := runlog.Heartbeat(sink, call, runlog.HeartbeatInterval)
		defer stopHB()
		msgs = galgame.PinPromptCache(msgs)
		options := []agentcore.CallOption{agentcore.WithThinking(thinking), agentcore.WithMaxTokens(galgame.MaxReplyTokens())}
		if key := galgame.ChatCacheKey(scope.SessionID); key != "" {
			options = append(options, agentcore.WithCallPromptCacheKey(key))
		}
		events, err := model.GenerateStream(ctx, msgs, nil, options...)
		if err != nil {
			stopHB()
			runlog.Finish(sink, call, err)
			return "", err
		}
		var text strings.Builder
		for ev := range events {
			switch ev.Type {
			case agentcore.StreamEventThinkingDelta:
				if ev.Delta != "" {
					runlog.MarkFirstToken(sink, call)
					if emit != nil {
						emit(galgame.Delta{Kind: "thinking", Text: ev.Delta})
					}
				}
			case agentcore.StreamEventTextDelta:
				if ev.Delta != "" {
					runlog.MarkFirstToken(sink, call)
					text.WriteString(ev.Delta)
					if emit != nil {
						emit(galgame.Delta{Kind: "text", Text: ev.Delta})
					}
				}
			case agentcore.StreamEventDone:
				call.ApplyResponse(&agentcore.LLMResponse{Message: ev.Message})
				if out := strings.TrimSpace(ev.Message.TextContent()); out != "" {
					text.Reset()
					text.WriteString(out)
				}
			case agentcore.StreamEventError:
				if ev.Err != nil {
					err = ev.Err
				}
			}
		}
		stopHB()
		runlog.Finish(sink, call, err)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(text.String()), nil
	}
}

func (h *Host) GalgameContextWindow() int {
	if h == nil || h.models == nil {
		return 0
	}
	provider, model, _ := h.models.CurrentSelection("galgame")
	window, _ := h.models.ResolveContextWindow(provider, model)
	return window
}
