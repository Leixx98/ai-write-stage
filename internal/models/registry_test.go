package models

import "testing"

func TestResolveDeepSeekFlash(t *testing.T) {
	r := NewModelRegistry()
	entry, ok := r.Resolve("deepseek-flash")
	if !ok {
		t.Fatal("deepseek-flash should resolve")
	}
	if entry.ContextWindow != 1048576 {
		t.Fatalf("window = %d", entry.ContextWindow)
	}
	if entry.InputCostPer1M != 0.15 || entry.OutputCostPer1M != 0.6 || entry.CacheReadCostPer1M != 0.003 {
		t.Fatalf("flash price = %+v", entry)
	}
	legacy, ok := r.Resolve("deepseek-v4-flash")
	if !ok {
		t.Fatal("deepseek-v4-flash should still resolve")
	}
	if legacy.InputCostPer1M != 0.15 || legacy.OutputCostPer1M != 0.6 {
		t.Fatalf("legacy flash price = %+v", legacy)
	}
}
