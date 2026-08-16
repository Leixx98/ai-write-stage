package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// GalgameCharacter is intentionally extensible: the first version only uses
// Prompt, while the remaining fields reserve the SillyTavern-compatible shape.
type GalgameCharacter struct {
	ID                      string         `json:"id"`
	Name                    string         `json:"name"`
	Prompt                  string         `json:"prompt"`
	Description             string         `json:"description,omitempty"`
	Personality             string         `json:"personality,omitempty"`
	Scenario                string         `json:"scenario,omitempty"`
	FirstMessage            string         `json:"first_mes,omitempty"`
	ExampleDialogue         string         `json:"mes_example,omitempty"`
	SystemPrompt            string         `json:"system_prompt,omitempty"`
	PostHistoryInstructions string         `json:"post_history_instructions,omitempty"`
	AlternateGreetings      []string       `json:"alternate_greetings,omitempty"`
	Extensions              map[string]any `json:"extensions,omitempty"`
	CreatedAt               time.Time      `json:"created_at,omitempty"`
	UpdatedAt               time.Time      `json:"updated_at,omitempty"`
}

type GalgameMessage struct {
	ID         string    `json:"id"`
	Role       string    `json:"role"`
	Name       string    `json:"name,omitempty"`
	Content    string    `json:"content"`
	ImageJobID string    `json:"image_job_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type GalgameSession struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	CharacterID     string           `json:"character_id"`
	StoryPreset     string           `json:"story_preset,omitempty"`
	UserPersona     string           `json:"user_persona,omitempty"`
	Messages        []GalgameMessage `json:"messages"`
	ImageWorkflowID string           `json:"image_workflow_id,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

type GalgameStore struct {
	io *IO
	mu sync.Mutex
}

func NewGalgameStore(io *IO) *GalgameStore { return &GalgameStore{io: io} }
func safeGalgameID(id string) bool {
	return strings.TrimSpace(id) != "" && filepath.Base(id) == id && !strings.ContainsAny(id, `/\\`)
}
func (s *GalgameStore) characterPath(id string) string {
	return filepath.ToSlash(filepath.Join("galgame/characters", id+".json"))
}
func (s *GalgameStore) sessionPath(id string) string {
	return filepath.ToSlash(filepath.Join("galgame/sessions", id+".json"))
}

func (s *GalgameStore) SaveCharacter(c GalgameCharacter) error {
	if !safeGalgameID(c.ID) {
		return fmt.Errorf("invalid character id")
	}
	if strings.TrimSpace(c.Prompt) == "" {
		return fmt.Errorf("character prompt is required")
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	c.UpdatedAt = time.Now().UTC()
	return s.io.WriteJSON(s.characterPath(c.ID), c)
}
func (s *GalgameStore) LoadCharacter(id string) (GalgameCharacter, error) {
	if !safeGalgameID(id) {
		return GalgameCharacter{}, fmt.Errorf("invalid character id")
	}
	var c GalgameCharacter
	err := s.io.ReadJSON(s.characterPath(id), &c)
	return c, err
}
func (s *GalgameStore) ListCharacters() ([]GalgameCharacter, error) {
	files, err := os.ReadDir(filepath.Join(s.io.dir, "galgame/characters"))
	if os.IsNotExist(err) {
		return []GalgameCharacter{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]GalgameCharacter, 0, len(files))
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			continue
		}
		c, e := s.LoadCharacter(strings.TrimSuffix(f.Name(), ".json"))
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, nil
}
func (s *GalgameStore) DeleteCharacter(id string) error {
	if !safeGalgameID(id) {
		return fmt.Errorf("invalid character id")
	}
	return s.io.RemoveFile(s.characterPath(id))
}

func (s *GalgameStore) SaveSession(session GalgameSession) error {
	if !safeGalgameID(session.ID) {
		return fmt.Errorf("invalid session id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	session.UpdatedAt = time.Now().UTC()
	return s.io.WriteJSON(s.sessionPath(session.ID), session)
}
func (s *GalgameStore) LoadSession(id string) (GalgameSession, error) {
	if !safeGalgameID(id) {
		return GalgameSession{}, fmt.Errorf("invalid session id")
	}
	var v GalgameSession
	err := s.io.ReadJSON(s.sessionPath(id), &v)
	return v, err
}
func (s *GalgameStore) ListSessions() ([]GalgameSession, error) {
	files, err := os.ReadDir(filepath.Join(s.io.dir, "galgame/sessions"))
	if os.IsNotExist(err) {
		return []GalgameSession{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]GalgameSession, 0, len(files))
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			continue
		}
		v, e := s.LoadSession(strings.TrimSuffix(f.Name(), ".json"))
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *GalgameStore) DeleteSession(id string) error {
	if !safeGalgameID(id) {
		return fmt.Errorf("invalid session id")
	}
	return s.io.RemoveFile(s.sessionPath(id))
}
