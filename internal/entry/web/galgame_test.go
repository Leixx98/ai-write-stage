package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/galgame"
	imagesvc "github.com/voocel/ainovel-cli/internal/imagejob/service"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestImportGalgameCharacterNormalizesAndSavesCard(t *testing.T) {
	roots := store.Open(t.TempDir(), "")
	controller := &v2Controller{tavern: roots.Tavern}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/galgame/characters/import", bytes.NewBufferString(`{"spec":"chara_card_v2","data":{"name":"林晚","description":"情报员","personality":"冷静","first_mes":"你好"}}`))
	recorder := httptest.NewRecorder()
	controller.importGalgameCharacter(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("import returned %d: %s", recorder.Code, recorder.Body.String())
	}
	characters, err := roots.Tavern.ListCharacters()
	if err != nil || len(characters) != 1 {
		t.Fatalf("characters = %#v, %v", characters, err)
	}
	if characters[0].Description != "情报员" || characters[0].Personality != "冷静" || characters[0].FirstMessage != "你好" {
		t.Fatalf("character = %#v", characters[0])
	}
}

func TestCreateGalgameSessionInitializesSelectedGreeting(t *testing.T) {
	roots := store.Open(t.TempDir(), "")
	character := store.GalgameCharacter{ID: "char", Name: "林晚", Description: "情报员", FirstMessage: "默认", AlternateGreetings: []string{"备用"}}
	if err := roots.Tavern.SaveCharacter(character); err != nil {
		t.Fatal(err)
	}
	controller := &v2Controller{tavern: roots.Tavern}
	body, _ := json.Marshal(map[string]any{"name": "测试会话", "character_id": "char", "user_persona": "旅人", "greeting_index": 1})
	recorder := httptest.NewRecorder()
	controller.galgameSessions(recorder, httptest.NewRequest(http.MethodPost, "/api/v2/galgame/sessions", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("create returned %d: %s", recorder.Code, recorder.Body.String())
	}
	sessions, err := roots.Tavern.ListSessions()
	if err != nil || len(sessions) != 1 || len(sessions[0].Messages) != 1 {
		t.Fatalf("sessions = %#v, %v", sessions, err)
	}
	if sessions[0].Messages[0].Content != "备用" || sessions[0].Messages[0].ID == "" {
		t.Fatalf("greeting = %#v", sessions[0].Messages[0])
	}
}

func TestGenerateGalgameReplyStreamsTextThenDone(t *testing.T) {
	t.Setenv("AINOVEL_HOME", t.TempDir())
	roots := store.Open(t.TempDir(), t.TempDir())
	character := store.GalgameCharacter{ID: "char", Name: "林晚", Description: "情报员"}
	if err := roots.Tavern.SaveCharacter(character); err != nil {
		t.Fatal(err)
	}
	session := store.GalgameSession{ID: "sess_1", Name: "测试", CharacterID: character.ID}
	if err := roots.Tavern.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	controller := &v2Controller{
		tavern:      roots.Tavern,
		images:      roots.Images,
		imageConfig: roots.ImageConfig,
		svc:         imagesvc.New(imagesvc.Config{Root: t.TempDir(), Jobs: roots.Images, Configuration: roots.ImageConfig, Providers: imagesvc.NewRegistry()}),
		chat: func(_ context.Context, _ []agentcore.Message, emit func(galgame.Delta)) (string, error) {
			emit(galgame.Delta{Kind: "thinking", Text: "想"})
			emit(galgame.Delta{Kind: "text", Text: "在"})
			return "在", nil
		},
	}
	body, _ := json.Marshal(map[string]string{"user_input": "看那边"})
	request := httptest.NewRequest(http.MethodPost, "/api/v2/galgame/sessions/sess_1/generate", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	controller.generateGalgameReply(recorder, request, "sess_1")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if ct := recorder.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	raw := recorder.Body.String()
	if !strings.Contains(raw, `"type":"thinking"`) || !strings.Contains(raw, `"type":"text"`) || !strings.Contains(raw, `"type":"done"`) {
		t.Fatalf("sse = %s", raw)
	}
	saved, err := roots.Tavern.LoadSession("sess_1")
	if err != nil || len(saved.Messages) != 2 || saved.Messages[1].Content != "在" {
		t.Fatalf("saved = %#v %v", saved, err)
	}
}

func TestManualGalgameImageSkipsSilentlyWhenChatImagesDisabled(t *testing.T) {
	t.Setenv("AINOVEL_HOME", t.TempDir())
	roots := store.Open(t.TempDir(), t.TempDir())
	character := store.GalgameCharacter{ID: "char", Name: "林晚", Description: "情报员"}
	if err := roots.Tavern.SaveCharacter(character); err != nil {
		t.Fatal(err)
	}
	session := store.GalgameSession{ID: "sess_1", Name: "测试", CharacterID: character.ID, Messages: []store.GalgameMessage{{ID: "msg_1", Role: "assistant", Content: "雨停了。"}}}
	if err := roots.Tavern.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	controller := &v2Controller{
		tavern: roots.Tavern, images: roots.Images, imageConfig: roots.ImageConfig,
		svc: imagesvc.New(imagesvc.Config{Root: t.TempDir(), Jobs: roots.Images, Configuration: roots.ImageConfig, Providers: imagesvc.NewRegistry()}),
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v2/galgame/sessions/sess_1/messages/msg_1/image", bytes.NewReader([]byte(`{}`)))
	controller.manualGalgameImage(recorder, request, "sess_1", "msg_1")
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	jobs, err := roots.Images.ListJobs()
	if err != nil || len(jobs) != 0 {
		t.Fatalf("disabled scene created jobs: %#v, %v", jobs, err)
	}
	stored, err := roots.Tavern.LoadSession(session.ID)
	if err != nil || stored.Messages[0].ImageJobID != "" {
		t.Fatalf("disabled scene changed message: %#v, %v", stored.Messages[0], err)
	}
}
