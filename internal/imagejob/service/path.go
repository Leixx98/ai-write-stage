package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/voocel/ainovel-cli/internal/imagejob"
	"github.com/voocel/ainovel-cli/internal/store"
)

func ImagePath(root string, job store.ImageJob) string {
	if job.Trigger == TriggerPlay {
		if playID := safeSessionID(job.PlayID); playID != "" && job.Ordinal > 0 {
			return filepath.Join(root, "galgame", "plays", playID, "images", fmt.Sprintf("%03d.png", job.Ordinal))
		}
		return filepath.Join(root, "meta", "images", "tests", job.JobID+".png")
	}
	if job.Trigger == TriggerGalgame {
		if sessionID := safeSessionID(job.SessionID); sessionID != "" {
			return filepath.Join(root, "galgame", "sessions", sessionID, "images", job.JobID+".png")
		}
		return filepath.Join(root, "meta", "images", "tests", job.JobID+".png")
	}
	if job.Trigger == TriggerUnit || (job.Chapter > 0 && job.Ordinal > 0) {
		return filepath.Join(root, "drafts", fmt.Sprintf("%02d.units", job.Chapter), fmt.Sprintf("%03d.png", job.Ordinal))
	}
	return filepath.Join(root, "meta", "images", "tests", job.JobID+".png")
}

func safeSessionID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || filepath.Base(id) != id || strings.ContainsAny(id, `/\\`) {
		return ""
	}
	return id
}

func UnitIdempotencyKey(request imagejob.PromptRequest, workflowID, workflowHash, schemaHash, promptFingerprint string) string {
	contentHash := sha256.Sum256([]byte(request.UnitText))
	parts := strings.Join([]string{request.UnitID, hex.EncodeToString(contentHash[:]), workflowID, workflowHash, schemaHash, promptFingerprint}, "\x00")
	sum := sha256.Sum256([]byte(parts))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func GalgameIdempotencyKey(sessionID string, request imagejob.PromptRequest, workflowID, workflowHash, schemaHash, promptFingerprint string) string {
	return "galgame:" + safeSessionID(sessionID) + ":" + UnitIdempotencyKey(request, workflowID, workflowHash, schemaHash, promptFingerprint)
}

func PlayIdempotencyKey(playID string, ordinal int, request imagejob.PromptRequest, workflowID, workflowHash, schemaHash, promptFingerprint string) string {
	return fmt.Sprintf("play:%s:%d:%s", safeSessionID(playID), ordinal, UnitIdempotencyKey(request, workflowID, workflowHash, schemaHash, promptFingerprint))
}

func hashJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func truncateBytes(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}
