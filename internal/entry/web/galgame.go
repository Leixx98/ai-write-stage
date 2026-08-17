package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/galgame"
	"github.com/voocel/ainovel-cli/internal/imagejob"
	imagesvc "github.com/voocel/ainovel-cli/internal/imagejob/service"
	"github.com/voocel/ainovel-cli/internal/store"
)

func galgameID(prefix string) string { return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano()) }

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
	var item store.GalgameSession
	if err := decodeBody(r, &item); err != nil {
		envelopeErr(w, 400, codeInvalidRequest, err)
		return
	}
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
	if parts[1] != "generate" || r.Method != http.MethodPost {
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("route not found"))
		return
	}
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
	now := time.Now().UTC()
	session.Messages = append(session.Messages, store.GalgameMessage{ID: galgameID("msg"), Role: "user", Content: input, CreatedAt: now})
	reply, err := galgame.Reply(r.Context(), c.chat, character, session, input)
	if err != nil {
		envelopeErr(w, 502, codeConflict, err)
		return
	}
	session.Messages = append(session.Messages, store.GalgameMessage{ID: galgameID("msg"), Role: "assistant", Name: character.Name, Content: reply, CreatedAt: time.Now().UTC()})
	if err := c.tavern.SaveSession(session); err != nil {
		envelopeErr(w, 500, codeConflict, err)
		return
	}
	imageJob, imageErr := c.startGalgameImage(session, character, reply)
	if imageErr == nil {
		session.Messages[len(session.Messages)-1].ImageJobID = imageJob.JobID
		_ = c.tavern.SaveSession(session)
	}
	data := map[string]any{"session": session, "reply": reply}
	if imageJob.JobID != "" {
		data["image_job"] = imageJob
	}
	if imageErr != nil {
		data["image_error"] = imageErr.Error()
	}
	envelope(w, 200, 0, data, "")
}

// startGalgameImage adapts conversation context to the same PromptRequest and
// ImageJob pipeline used by novel writing units.
func (c *v2Controller) startGalgameImage(session store.GalgameSession, character store.GalgameCharacter, reply string) (store.ImageJob, error) {
	bridge, err := c.media.LoadBridgeConfig()
	if err != nil {
		return store.ImageJob{}, fmt.Errorf("图片生成桥接配置无法读取")
	}
	if !bridge.Enabled {
		return store.ImageJob{}, fmt.Errorf("图片生成桥接尚未启用")
	}
	workflowID := strings.TrimSpace(session.ImageWorkflowID)
	if workflowID == "" {
		workflowID = bridge.WorkflowID
	}
	workflow, err := c.media.LoadWorkflow(workflowID)
	if err != nil {
		return store.ImageJob{}, fmt.Errorf("图片工作流不存在")
	}
	canvas, err := c.media.LoadOrCreateWorkflowCanvas(workflow.ID)
	if err != nil {
		return store.ImageJob{}, err
	}
	promptSchema, err := imagejob.BuildPromptSchema(workflow.ID, canvas)
	if err != nil {
		return store.ImageJob{}, err
	}
	if len(promptSchema.Fields) == 0 {
		return store.ImageJob{}, fmt.Errorf("请先在画布中勾选要发给提示词模型的字段")
	}
	schemaJSON, err := json.Marshal(promptSchema.Schema)
	if err != nil {
		return store.ImageJob{}, err
	}
	plan := "角色设定：\n" + character.Prompt
	if strings.TrimSpace(session.StoryPreset) != "" {
		plan += "\n\n剧情预设：\n" + session.StoryPreset
	}
	var history strings.Builder
	end := len(session.Messages) - 1
	start := end - 6
	if start < 0 {
		start = 0
	}
	for _, message := range session.Messages[start:end] {
		fmt.Fprintf(&history, "%s：%s\n", message.Role, message.Content)
	}
	request := imagejob.PromptRequest{
		UnitID: session.Messages[len(session.Messages)-1].ID, ChapterTitle: character.Name, UnitPlan: plan,
		UnitText: reply, PreviousTail: tailText(history.String(), bridge.PreviousTailChars),
		Schema: schemaJSON, SchemaHash: promptSchema.SchemaHash, SystemPrompt: promptSchema.Composed,
	}
	return c.svc.StartGalgame(session.ID, imagesvc.PromptRun{
		Request: request, Workflow: workflow, Canvas: canvas, Bridge: bridge, Schema: promptSchema,
	})
}
