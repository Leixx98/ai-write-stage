package host

import "testing"

func TestFillContextStatusUsesModelWindow(t *testing.T) {
	h := &Host{}
	snap := RuntimeSnapshot{ModelContextWindow: 1048576}
	h.fillContextStatus(&snap)
	if snap.ContextWindow != 1048576 || snap.ContextTokens != 0 {
		t.Fatalf("window/tokens = %d/%d", snap.ContextWindow, snap.ContextTokens)
	}
}

func TestFillContextStatusPrefersBusiestAgent(t *testing.T) {
	h := &Host{}
	snap := RuntimeSnapshot{
		ModelContextWindow: 200000,
		Agents: []AgentSnapshot{
			{Name: "editor", Context: AgentContextSnapshot{Tokens: 1000, ContextWindow: 200000, Percent: 0.5}},
			{Name: "writer", Context: AgentContextSnapshot{Tokens: 80000, ContextWindow: 1048576, Percent: 7.6, Scope: "writer", Strategy: "keep"}},
		},
	}
	h.fillContextStatus(&snap)
	if snap.ContextTokens != 80000 || snap.ContextWindow != 1048576 {
		t.Fatalf("context = %d/%d", snap.ContextTokens, snap.ContextWindow)
	}
	if snap.ContextScope != "writer" || snap.ContextStrategy != "keep" {
		t.Fatalf("scope/strategy = %q/%q", snap.ContextScope, snap.ContextStrategy)
	}
	if snap.ContextPercent < 7 || snap.ContextPercent > 8 {
		t.Fatalf("percent = %v", snap.ContextPercent)
	}
}
