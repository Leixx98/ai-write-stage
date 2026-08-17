package host

import (
	"context"
	"fmt"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/galgame"
)

func (h *Host) NewGalgameGenerate() galgame.GenerateFunc {
	return func(ctx context.Context, msgs []agentcore.Message) (string, error) {
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
		h.mu.Unlock()
		response, err := model.Generate(ctx, msgs, nil, agentcore.WithThinking(thinking), agentcore.WithMaxTokens(galgame.MaxReplyTokens()))
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(response.Message.TextContent()), nil
	}
}

// GalgameContextWindow resolves the currently selected model on every call so
// prompt trimming follows runtime model switches.
func (h *Host) GalgameContextWindow() int {
	if h == nil || h.models == nil {
		return 0
	}
	provider, model, _ := h.models.CurrentSelection("galgame")
	window, _ := h.models.ResolveContextWindow(provider, model)
	return window
}
