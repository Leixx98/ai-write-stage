package host

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/imagejob"
	"github.com/voocel/ainovel-cli/internal/store"
)

func (h *Host) startCompletedUnitWatcher() {
	h.asyncWG.Add(1)
	go func() {
		defer h.asyncWG.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		attempted := make(map[string]time.Time)
		for {
			select {
			case <-h.runCtx.Done():
				return
			case <-ticker.C:
				h.enqueueCompletedUnitImages(attempted)
			}
		}
	}()
}

func (h *Host) enqueueCompletedUnitImages(attempted map[string]time.Time) {
	settings, err := h.roots.ImageConfig.LoadSettings()
	if err != nil || !settings.Novel.Enabled || !settings.Novel.AutoGenerate {
		return
	}
	outline, err := h.store.Outline.LoadOutline()
	if err != nil {
		slog.Warn("读取大纲以协调单元配图失败", "module", "host.unit_images", "err", err)
		return
	}
	for _, chapter := range outline {
		progress, progressErr := h.store.Drafts.LoadWritingProgress(chapter.Chapter)
		if progressErr != nil || progress == nil {
			continue
		}
		for ordinal := 1; ordinal <= progress.CompletedUnits; ordinal++ {
			key := fmt.Sprintf("%d/%d", chapter.Chapter, ordinal)
			if retryAt, exists := attempted[key]; exists {
				if retryAt.IsZero() || time.Now().Before(retryAt) {
					continue
				}
			}
			if _, err := h.startUnitImage(chapter.Chapter, ordinal, "", false, false); err != nil {
				attempted[key] = time.Now().Add(15 * time.Second)
				slog.Warn("自动单元配图协调失败", "module", "host.unit_images", "unit", key, "err", err)
				continue
			}
			attempted[key] = time.Time{}
		}
	}
}

func (h *Host) StartUnitImage(chapter, ordinal int, profileID string, force bool) (store.ImageJob, error) {
	return h.startUnitImage(chapter, ordinal, profileID, force, true)
}

func (h *Host) startUnitImage(chapter, ordinal int, profileID string, force, manual bool) (store.ImageJob, error) {
	request, err := h.unitSceneImageRequest(chapter, ordinal)
	if err != nil {
		return store.ImageJob{}, err
	}
	request.ProfileID = strings.TrimSpace(profileID)
	request.Force = force
	request.Manual = manual
	job, _, err := h.ensureImageService().Start(request)
	return job, err
}

func (h *Host) unitSceneImageRequest(chapter, ordinal int) (imagejob.SceneImageRequest, error) {
	unitText, err := h.store.Drafts.LoadWritingUnit(chapter, ordinal)
	if err != nil {
		return imagejob.SceneImageRequest{}, err
	}
	if strings.TrimSpace(unitText) == "" {
		return imagejob.SceneImageRequest{}, fmt.Errorf("writing unit %d/%d is empty", chapter, ordinal)
	}
	plan, err := h.store.Drafts.LoadChapterPlan(chapter)
	if err != nil || plan == nil {
		return imagejob.SceneImageRequest{}, fmt.Errorf("chapter %d plan does not exist", chapter)
	}
	assignments := plan.WritingUnits()
	if ordinal <= 0 || ordinal > len(assignments) {
		return imagejob.SceneImageRequest{}, fmt.Errorf("writing unit ordinal is outside the chapter plan")
	}
	assignment := assignments[ordinal-1]
	planJSON, err := json.Marshal(assignment)
	if err != nil {
		return imagejob.SceneImageRequest{}, err
	}
	previousTail := ""
	if ordinal > 1 {
		if previous, readErr := h.store.Drafts.LoadWritingUnit(chapter, ordinal-1); readErr == nil {
			previousTail = tailRuntimeText(previous, 2000)
		}
	}
	return imagejob.SceneImageRequest{
		Scene: imagejob.SceneNovel, SceneID: fmt.Sprintf("chapter-%d-unit-%d", chapter, ordinal),
		UnitID: assignment.Unit.ID, Chapter: chapter, Ordinal: ordinal, Title: plan.Title,
		Text: unitText, PreviousText: previousTail, VisualIntent: string(planJSON),
	}, nil
}

func tailRuntimeText(value string, maximum int) string {
	runes := []rune(value)
	if maximum <= 0 || len(runes) <= maximum {
		return value
	}
	return string(runes[len(runes)-maximum:])
}
