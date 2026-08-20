package web

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/galgame"
	"github.com/voocel/ainovel-cli/internal/galgame/runlog"
	"github.com/voocel/ainovel-cli/internal/imagejob"
	"github.com/voocel/ainovel-cli/internal/store"
)

func galgameID(prefix string) string { return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano()) }

func (c *v2Controller) importGalgameCharacter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<20+1))
	if err != nil {
		envelopeErr(w, 400, codeInvalidRequest, err)
		return
	}
	if len(raw) > 4<<20 {
		envelopeErr(w, 413, codeInvalidRequest, fmt.Errorf("character card exceeds 4 MiB"))
		return
	}
	item, err := galgame.ImportCharacterJSON(raw)
	if err != nil {
		envelopeErr(w, 422, codeInvalidRequest, err)
		return
	}
	item.CreatedAt = time.Now().UTC()
	item.ID = c.tavern.NewCharacterID(item.Name, item.CreatedAt)
	if err := c.tavern.SaveCharacter(item); err != nil {
		envelopeErr(w, 422, codeInvalidRequest, err)
		return
	}
	envelope(w, 201, 0, item, "")
}

func (c *v2Controller) galgameCharacters(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := c.tavern.ListCharacters()
		if err != nil {
			envelopeErr(w, 500, codeConflict, err)
			return
		}
		envelope(w, 200, 0, items, "")
	case http.MethodPost:
		var item store.GalgameCharacter
		if err := decodeBody(r, &item); err != nil {
			envelopeErr(w, 400, codeInvalidRequest, err)
			return
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = time.Now().UTC()
		}
		if item.ID == "" {
			item.ID = c.tavern.NewCharacterID(item.Name, item.CreatedAt)
		}
		if err := c.tavern.SaveCharacter(item); err != nil {
			envelopeErr(w, 422, codeInvalidRequest, err)
			return
		}
		envelope(w, 200, 0, item, "")
	default:
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
	}
}

func (c *v2Controller) galgameCharacter(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method == http.MethodDelete {
		if err := c.tavern.DeleteCharacter(id); err != nil {
			envelopeErr(w, 404, codeNotFound, err)
			return
		}
		envelope(w, 200, 0, map[string]any{"deleted": true}, "")
		return
	}
	if r.Method == http.MethodGet {
		item, err := c.tavern.LoadCharacter(id)
		if err != nil {
			envelopeErr(w, 404, codeNotFound, err)
			return
		}
		envelope(w, 200, 0, item, "")
		return
	}
	if r.Method == http.MethodPut {
		var item store.GalgameCharacter
		if err := decodeBody(r, &item); err != nil {
			envelopeErr(w, 400, codeInvalidRequest, err)
			return
		}
		item.ID = id
		if err := c.tavern.SaveCharacter(item); err != nil {
			envelopeErr(w, 422, codeInvalidRequest, err)
			return
		}
		envelope(w, 200, 0, item, "")
		return
	}
	envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
}

func (c *v2Controller) galgameSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		items, err := c.tavern.ListSessions()
		if err != nil {
			envelopeErr(w, 500, codeConflict, err)
			return
		}
		envelope(w, 200, 0, items, "")
		return
	}
	if r.Method != http.MethodPost {
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	var request struct {
		store.GalgameSession
		GreetingIndex int `json:"greeting_index"`
	}
	if err := decodeBody(r, &request); err != nil {
		envelopeErr(w, 400, codeInvalidRequest, err)
		return
	}
	item := request.GalgameSession
	if strings.TrimSpace(item.CharacterID) == "" {
		envelopeErr(w, 422, codeInvalidRequest, fmt.Errorf("character_id is required"))
		return
	}
	character, err := c.tavern.LoadCharacter(item.CharacterID)
	if err != nil {
		envelopeErr(w, 422, codeInvalidRequest, err)
		return
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	if item.ID == "" {
		item.ID = c.tavern.NewSessionID(character.Name, item.Name, item.CreatedAt)
	}
	item = galgame.InitializeSession(character, item, item.CreatedAt, request.GreetingIndex)
	for index := range item.Messages {
		if item.Messages[index].ID == "" {
			item.Messages[index].ID = galgameID("msg")
		}
	}
	if err := c.tavern.SaveSession(item); err != nil {
		envelopeErr(w, 422, codeInvalidRequest, err)
		return
	}
	envelope(w, 200, 0, item, "")
}

func (c *v2Controller) galgameSession(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	if id == "" {
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("session not found"))
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			item, err := c.tavern.LoadSession(id)
			if err != nil {
				envelopeErr(w, 404, codeNotFound, err)
				return
			}
			envelope(w, 200, 0, item, "")
		case http.MethodPut:
			item, err := c.tavern.LoadSession(id)
			if err != nil {
				envelopeErr(w, 404, codeNotFound, err)
				return
			}
			var update struct {
				Name           string `json:"name"`
				UserPersona    string `json:"user_persona"`
				ImageProfileID string `json:"image_profile_id"`
			}
			if err := decodeBody(r, &update); err != nil {
				envelopeErr(w, 400, codeInvalidRequest, err)
				return
			}
			if strings.TrimSpace(update.Name) != "" {
				item.Name = strings.TrimSpace(update.Name)
			}
			item.UserPersona = strings.TrimSpace(update.UserPersona)
			item.ImageProfileID = strings.TrimSpace(update.ImageProfileID)
			if err := c.tavern.SaveSession(item); err != nil {
				envelopeErr(w, 500, codeConflict, err)
				return
			}
			envelope(w, 200, 0, item, "")
		case http.MethodDelete:
			if err := c.tavern.DeleteSession(id); err != nil {
				envelopeErr(w, 404, codeNotFound, err)
				return
			}
			envelope(w, 200, 0, map[string]any{"deleted": true}, "")
		default:
			envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
		}
		return
	}
	if strings.HasPrefix(parts[1], "messages/") && strings.HasSuffix(parts[1], "/image") && r.Method == http.MethodPost {
		messageID := strings.TrimSuffix(strings.TrimPrefix(parts[1], "messages/"), "/image")
		c.manualGalgameImage(w, r, id, messageID)
		return
	}
	if parts[1] != "generate" || r.Method != http.MethodPost {
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("route not found"))
		return
	}
	c.generateGalgameReply(w, r, id)
}

func (c *v2Controller) manualGalgameImage(w http.ResponseWriter, r *http.Request, sessionID, messageID string) {
	session, err := c.tavern.LoadSession(sessionID)
	if err != nil {
		envelopeErr(w, http.StatusNotFound, codeNotFound, err)
		return
	}
	character, err := c.tavern.LoadCharacter(session.CharacterID)
	if err != nil {
		envelopeErr(w, http.StatusUnprocessableEntity, codeInvalidRequest, err)
		return
	}
	messageIndex := -1
	for index, message := range session.Messages {
		if message.ID == messageID && message.Role == "assistant" {
			messageIndex = index
			break
		}
	}
	if messageIndex < 0 {
		envelopeErr(w, http.StatusNotFound, codeNotFound, fmt.Errorf("assistant message not found"))
		return
	}
	session.Messages = append([]store.GalgameMessage(nil), session.Messages[:messageIndex+1]...)
	job, err := c.startGalgameImageRequest(session, character, session.Messages[messageIndex].Content, true)
	if err != nil {
		c.writeImageServiceErr(w, err)
		return
	}
	if job.JobID != "" {
		full, loadErr := c.tavern.LoadSession(sessionID)
		if loadErr == nil {
			for index := range full.Messages {
				if full.Messages[index].ID == messageID {
					full.Messages[index].ImageJobID = job.JobID
					break
				}
			}
			_ = c.tavern.SaveSession(full)
		}
	}
	envelope(w, http.StatusAccepted, 0, job, "")
}

func (c *v2Controller) generateGalgameReply(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		UserInput string `json:"user_input"`
	}
	if err := decodeBody(r, &req); err != nil {
		envelopeErr(w, 400, codeInvalidRequest, err)
		return
	}
	session, err := c.tavern.LoadSession(id)
	if err != nil {
		envelopeErr(w, 404, codeNotFound, err)
		return
	}
	character, err := c.tavern.LoadCharacter(session.CharacterID)
	if err != nil {
		envelopeErr(w, 422, codeInvalidRequest, err)
		return
	}
	input := strings.TrimSpace(req.UserInput)
	if input == "" {
		envelopeErr(w, 422, codeInvalidRequest, fmt.Errorf("user_input is required"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		envelopeErr(w, 500, codeConflict, fmt.Errorf("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	contextWindow := 0
	if c.rt != nil {
		contextWindow = c.rt.GalgameContextWindow()
	}
	reply, err := galgame.ReplyStream(runlog.WithChat(r.Context(), session.ID, session.CharacterID), c.chat, character, &session, input, galgame.ReplyOptions{ContextWindow: contextWindow}, func(delta galgame.Delta) {
		_ = writeChatSSE(w, flusher, map[string]any{"type": delta.Kind, "delta": delta.Text})
	})
	if err != nil {
		_ = writeChatSSE(w, flusher, map[string]any{"type": "error", "error": err.Error()})
		return
	}
	now := time.Now().UTC()
	session.Messages = append(session.Messages, store.GalgameMessage{ID: galgameID("msg"), Role: "user", Content: input, CreatedAt: now})
	assistant := store.GalgameMessage{ID: galgameID("msg"), Role: "assistant", Name: character.Name, Content: reply, CreatedAt: time.Now().UTC()}
	if assistant.ID == session.Messages[len(session.Messages)-1].ID {
		assistant.ID += "_a"
	}
	session.Messages = append(session.Messages, assistant)
	if err := c.tavern.SaveSession(session); err != nil {
		_ = writeChatSSE(w, flusher, map[string]any{"type": "error", "error": err.Error()})
		return
	}
	imageJob, imageErr := c.startGalgameImage(session, character, reply)
	if imageErr == nil {
		session.Messages[len(session.Messages)-1].ImageJobID = imageJob.JobID
		_ = c.tavern.SaveSession(session)
	}
	done := map[string]any{"type": "done", "session": session, "reply": reply}
	if imageJob.JobID != "" {
		done["image_job"] = imageJob
	}
	if imageErr != nil {
		done["image_error"] = imageErr.Error()
	}
	_ = writeChatSSE(w, flusher, done)
}

func writeChatSSE(w http.ResponseWriter, flusher http.Flusher, payload any) bool {
	if !writeSSE(w, payload) {
		return false
	}
	flusher.Flush()
	return true
}

// startGalgameImage adapts conversation context to the same PromptRequest and
// ImageJob pipeline used by novel writing units.
func (c *v2Controller) startGalgameImage(session store.GalgameSession, character store.GalgameCharacter, reply string) (store.ImageJob, error) {
	return c.startGalgameImageRequest(session, character, reply, false)
}

func (c *v2Controller) startGalgameImageRequest(session store.GalgameSession, character store.GalgameCharacter, reply string, manual bool) (store.ImageJob, error) {
	assistantIndex := 0
	for _, message := range session.Messages {
		if message.Role == "assistant" {
			assistantIndex++
		}
	}
	var history strings.Builder
	end := len(session.Messages) - 1
	start := end - 6
	if start < 0 {
		start = 0
	}
	for _, message := range session.Messages[start:end] {
		fmt.Fprintf(&history, "%s: %s\n", message.Role, message.Content)
	}
	request := imagejob.SceneImageRequest{
		Scene: imagejob.SceneChat, SceneID: session.ID, UnitID: session.Messages[len(session.Messages)-1].ID,
		AssistantIndex: assistantIndex, Title: character.Name, Dialogue: reply,
		PreviousText: tailText(history.String(), 2000),
		VisualIntent: strings.TrimSpace(strings.Join([]string{character.Personality, character.Scenario}, "\n")),
		Characters:   []imagejob.CharacterContext{{Name: character.Name, Description: character.Description}},
		ProfileID:    session.ImageProfileID, Manual: manual,
	}
	job, skipped, err := c.svc.Start(request)
	if skipped {
		return store.ImageJob{}, nil
	}
	return job, err
}
