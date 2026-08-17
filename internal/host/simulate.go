package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/voocel/ainovel-cli/internal/host/sim"
)

// Simulate 读取 simulate 目录并生成或增量更新仿写画像。
func (h *Host) Simulate(ctx context.Context) (<-chan sim.Event, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working dir: %w", err)
	}
	return h.SimulateFrom(ctx, filepath.Join(wd, "simulate"))
}

// SimulateFrom runs the imitation pipeline against an explicit source
// directory. The operation remains owned by Host so exclusivity is enforced.
func (h *Host) SimulateFrom(ctx context.Context, sourceDir string) (<-chan sim.Event, error) {
	if err := h.acquireExclusive("生成仿写画像"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()

	sourceDir = strings.TrimSpace(sourceDir)
	if sourceDir == "" {
		h.releaseExclusive()
		return nil, fmt.Errorf("simulation source directory is required")
	}
	deps := sim.Deps{
		Store: h.store,
		LLM:   h.models.ForRole("architect"),
		Prompts: sim.Prompts{
			Source: h.bundle.Prompts.SimulationSource,
			Merge:  h.bundle.Prompts.SimulationMerge,
		},
	}
	ch, err := sim.Run(ctx, deps, sim.Options{SourceDir: sourceDir})
	if err != nil {
		h.releaseExclusive()
		return nil, err
	}
	return superviseExclusive(h, ch), nil
}

// StartSimulation is the Host-owned async entry used by web clients.
func (h *Host) StartSimulation(sourceDir string) error {
	ch, err := h.SimulateFrom(context.Background(), sourceDir)
	if err != nil {
		return err
	}
	if !h.launchAsync(func() {
		for ev := range ch {
			summary := ev.Message
			level := ""
			if ev.Err != nil {
				level = "error"
				summary = fmt.Sprintf("%s: %v", summary, ev.Err)
			}
			h.emitEvent(Event{Time: ev.Time, Category: "SIMULATE", Summary: summary, Level: level})
		}
	}) {
		return fmt.Errorf("Host is closing; cannot start simulation")
	}
	return nil
}

// ImportSimulationProfile 导入此前生成的仿写画像。
func (h *Host) ImportSimulationProfile(ctx context.Context, path string) (<-chan sim.Event, error) {
	if err := h.acquireExclusive("导入仿写画像"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()
	ch, err := sim.RunImport(ctx, h.store, path)
	if err != nil {
		h.releaseExclusive()
		return nil, err
	}
	return superviseExclusive(h, ch), nil
}
