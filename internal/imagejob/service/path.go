package service

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Leixx98/ai-write-stage/internal/imagejob"
	"github.com/Leixx98/ai-write-stage/internal/store"
)

func ImagePath(root string, job store.ImageJob) string {
	if job.Scene == imagejob.ScenePlay || job.Trigger == TriggerPlay {
		if playID := safeSessionID(job.PlayID); playID != "" && job.Ordinal > 0 {
			return filepath.Join(root, store.TavernDirName, "plays", playID, "images", fmt.Sprintf("%03d.png", job.Ordinal))
		}
		return filepath.Join(root, store.NovelDirName, "meta", "images", "tests", job.JobID+".png")
	}
	if job.Scene == imagejob.SceneChat || job.Trigger == TriggerGalgame {
		if sessionID := safeSessionID(job.SessionID); sessionID != "" {
			return filepath.Join(root, store.TavernDirName, "sessions", sessionID, "images", job.JobID+".png")
		}
		return filepath.Join(root, store.NovelDirName, "meta", "images", "tests", job.JobID+".png")
	}
	if job.Scene == imagejob.SceneNovel || job.Trigger == TriggerUnit || (job.Chapter > 0 && job.Ordinal > 0) {
		return filepath.Join(root, store.NovelDirName, "drafts", fmt.Sprintf("%02d.units", job.Chapter), fmt.Sprintf("%03d.png", job.Ordinal))
	}
	return filepath.Join(root, store.NovelDirName, "meta", "images", "tests", job.JobID+".png")
}

func safeSessionID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || filepath.Base(id) != id || strings.ContainsAny(id, `/\\`) {
		return ""
	}
	return id
}

func truncateBytes(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}
