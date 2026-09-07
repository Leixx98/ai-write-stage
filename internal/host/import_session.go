package host

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Leixx98/ai-write-stage/internal/host/imp"
)

const importHistoryLimit int = 200

type ImportSessionState string

const (
	ImportSessionIdle                 ImportSessionState = "idle"
	ImportSessionRunning              ImportSessionState = "running"
	ImportSessionAwaitingConfirmation ImportSessionState = "awaiting_confirmation"
	ImportSessionAwaitingStoryStatus  ImportSessionState = "awaiting_story_status"
	ImportSessionPaused               ImportSessionState = "paused"
	ImportSessionCompleted            ImportSessionState = "completed"
	ImportSessionFailed               ImportSessionState = "failed"
	ImportSessionCancelled            ImportSessionState = "cancelled"
)

type ImportProgress struct {
	Time      time.Time `json:"time"`
	Stage     imp.Stage `json:"stage"`
	Current   int       `json:"current"`
	Total     int       `json:"total"`
	Message   string    `json:"message"`
	Level     string    `json:"level,omitempty"`
	Error     string    `json:"error,omitempty"`
	Continued bool      `json:"continued,omitempty"`
}

type ImportSessionStatus struct {
	ID            string             `json:"id,omitempty"`
	State         ImportSessionState `json:"state"`
	SourcePath    string             `json:"source_path,omitempty"`
	StoryStatus   string             `json:"story_status,omitempty"`
	Guidance      string             `json:"guidance,omitempty"`
	ContinueAfter bool               `json:"continue_after"`
	Preview       string             `json:"preview,omitempty"`
	Message       string             `json:"message,omitempty"`
	History       []ImportProgress   `json:"history"`
	StartedAt     time.Time          `json:"started_at,omitempty"`
	UpdatedAt     time.Time          `json:"updated_at,omitempty"`
	CanResume     bool               `json:"can_resume"`
}

type importSessionManager struct {
	mu     sync.RWMutex
	status ImportSessionStatus
	cancel context.CancelFunc
}

func newImportSessionManager() *importSessionManager {
	return &importSessionManager{status: ImportSessionStatus{State: ImportSessionIdle, History: []ImportProgress{}}}
}

func (m *importSessionManager) snapshot() ImportSessionStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := m.status
	status.History = append([]ImportProgress(nil), m.status.History...)
	return status
}

func (m *importSessionManager) begin(opts imp.Options, cancel context.CancelFunc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.State == ImportSessionRunning {
		return fmt.Errorf("an import session is already running")
	}
	now := time.Now().UTC()
	history := append([]ImportProgress(nil), m.status.History...)
	sourcePath := strings.TrimSpace(opts.SourcePath)
	sessionID := m.status.ID
	startedAt := m.status.StartedAt
	if sourcePath != "" {
		sessionID = fmt.Sprintf("import-%d", now.UnixNano())
		startedAt = now
		history = []ImportProgress{}
	}
	if sourcePath == "" {
		sourcePath = m.status.SourcePath
	}
	storyStatus := strings.TrimSpace(opts.StoryResolution)
	if storyStatus == "" {
		storyStatus = m.status.StoryStatus
	}
	guidance := strings.TrimSpace(opts.Guidance)
	if guidance == "" && !opts.ResetGuidance {
		guidance = m.status.Guidance
	}
	if startedAt.IsZero() {
		startedAt = now
	}
	if sessionID == "" {
		sessionID = fmt.Sprintf("import-%d", now.UnixNano())
	}
	m.status = ImportSessionStatus{ID: sessionID, State: ImportSessionRunning, SourcePath: sourcePath, StoryStatus: storyStatus, Guidance: guidance, ContinueAfter: opts.ContinueAfter || m.status.ContinueAfter, History: history, StartedAt: startedAt, UpdatedAt: now}
	m.cancel = cancel
	return nil
}

func (m *importSessionManager) appendEvent(event imp.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	progress := ImportProgress{Time: event.Time, Stage: event.Stage, Current: event.Current, Total: event.Total, Message: event.Message, Level: event.Level, Continued: event.Continued}
	if event.Err != nil {
		progress.Error = event.Err.Error()
	}
	m.status.History = append(m.status.History, progress)
	if len(m.status.History) > importHistoryLimit {
		m.status.History = append([]ImportProgress(nil), m.status.History[len(m.status.History)-importHistoryLimit:]...)
	}
	m.status.Message = event.Message
	m.status.UpdatedAt = time.Now().UTC()
	switch event.Stage {
	case imp.StageAwaitingConfirmation:
		if event.RequiresAction {
			m.status.State = ImportSessionAwaitingConfirmation
			m.status.Preview = event.Message
			m.status.CanResume = true
		} else {
			m.status.State = ImportSessionRunning
			m.status.Preview = ""
			m.status.CanResume = false
		}
	case imp.StageAwaitingStoryStatus:
		if event.RequiresAction {
			m.status.State = ImportSessionAwaitingStoryStatus
			m.status.CanResume = true
		} else {
			m.status.State = ImportSessionRunning
			m.status.CanResume = false
		}
	case imp.StageDone:
		m.status.State = ImportSessionCompleted
		m.status.CanResume = false
	case imp.StageError:
		if m.status.State != ImportSessionCancelled {
			m.status.State = ImportSessionFailed
		}
		m.status.CanResume = true
	default:
		m.status.State = ImportSessionRunning
		m.status.CanResume = false
	}
	if event.RequiresAction || event.Stage == imp.StageDone || event.Stage == imp.StageError {
		m.cancel = nil
	}
}

func (m *importSessionManager) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	m.status.State = ImportSessionFailed
	m.status.Message = err.Error()
	m.status.CanResume = true
	m.status.UpdatedAt = now
	m.status.History = append(m.status.History, ImportProgress{Time: now, Stage: imp.StageError, Message: "Import failed", Level: "error", Error: err.Error()})
	if len(m.status.History) > importHistoryLimit {
		m.status.History = append([]ImportProgress(nil), m.status.History[len(m.status.History)-importHistoryLimit:]...)
	}
	m.cancel = nil
}

func (m *importSessionManager) cancelRun() bool {
	m.mu.Lock()
	cancel := m.cancel
	if cancel == nil || m.status.State != ImportSessionRunning {
		m.mu.Unlock()
		return false
	}
	now := time.Now().UTC()
	m.status.State = ImportSessionCancelled
	m.status.Message = "Import cancelled"
	m.status.CanResume = true
	m.status.UpdatedAt = now
	m.cancel = nil
	m.mu.Unlock()
	cancel()
	return true
}

func (m *importSessionManager) restore(state ImportSessionState, message string, preview string) ImportSessionStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.State != ImportSessionIdle {
		status := m.status
		status.History = append([]ImportProgress(nil), m.status.History...)
		return status
	}
	now := time.Now().UTC()
	m.status.State = state
	m.status.Message = message
	m.status.Preview = preview
	m.status.CanResume = state != ImportSessionCompleted && state != ImportSessionIdle
	m.status.UpdatedAt = now
	status := m.status
	status.History = append([]ImportProgress(nil), m.status.History...)
	return status
}
