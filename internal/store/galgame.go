package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

// GalgameCharacter stores the supported subset of a SillyTavern character
// card. Extensions are preserved for future features but are not interpreted.
type GalgameCharacter struct {
	ID                      string         `json:"id"`
	Name                    string         `json:"name"`
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
	UserPersona     string           `json:"user_persona,omitempty"`
	Messages        []GalgameMessage `json:"messages"`
	HistoryCutoff   int              `json:"history_cutoff,omitempty"`
	ImageWorkflowID string           `json:"image_workflow_id,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

type GalgameStore struct {
	io *IO
	mu sync.Mutex

	// logMu 串行化酒馆/剧场日志的追加、轮转与尾读：大小/代际记账、SSE 快照与
	// 增量去重都依赖它提供的原子性。与 io.mu 分层（先 logMu 后 io.mu，无反向）。
	logMu       sync.Mutex
	logState    map[string]*tavernLogState
	logObserver TavernLogObserver
}

func NewGalgameStore(io *IO) *GalgameStore { return &GalgameStore{io: io} }
func safeGalgameID(id string) bool {
	return strings.TrimSpace(id) != "" && filepath.Base(id) == id && !strings.ContainsAny(id, `/\\`)
}
func (s *GalgameStore) NewCharacterID(name string, createdAt time.Time) string {
	return s.uniqueFileID(s.characterPath, joinGalgameID(createdAt, sanitizeGalgameName(name, "character")))
}
func (s *GalgameStore) NewSessionID(characterName, sessionName string, createdAt time.Time) string {
	return s.uniqueFileID(s.sessionPath, joinGalgameID(createdAt, sanitizeGalgameName(characterName, "character"), sanitizeGalgameName(sessionName, "session")))
}
func (s *GalgameStore) uniqueFileID(pathFn func(string) string, base string) string {
	if s.fileMissing(pathFn(base)) {
		return base
	}
	for i := 2; i < 10000; i++ {
		id := fmt.Sprintf("%s_%d", base, i)
		if s.fileMissing(pathFn(id)) {
			return id
		}
	}
	return fmt.Sprintf("%s_%d", base, time.Now().UnixNano())
}
func (s *GalgameStore) fileMissing(rel string) bool {
	_, err := os.Stat(s.io.path(rel))
	return os.IsNotExist(err)
}
func joinGalgameID(createdAt time.Time, parts ...string) string {
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	cleaned := make([]string, 0, len(parts)+1)
	for _, part := range parts {
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) == 0 {
		cleaned = append(cleaned, "untitled")
	}
	return strings.Join(cleaned, "_") + "_" + createdAt.UTC().Format("20060102_150405")
}
func sanitizeGalgameName(name, fallback string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.TrimSpace(name) {
		if r < 32 || r == 127 || r == '%' || unicode.IsSpace(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
			continue
		}
		b.WriteRune(r)
		lastUnderscore = false
	}
	s := strings.Trim(b.String(), "._")
	if s == "" {
		s = fallback
	}
	return truncateRunes(s, 40)
}
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	i := 0
	for idx := range s {
		if i == n {
			return s[:idx]
		}
		i++
	}
	return s
}
func (s *GalgameStore) characterPath(id string) string {
	return filepath.ToSlash(filepath.Join("galgame/characters", id+".json"))
}
func (s *GalgameStore) sessionPath(id string) string {
	return filepath.ToSlash(filepath.Join("galgame/sessions", id+".json"))
}
func (s *GalgameStore) sessionDir(id string) string {
	return filepath.ToSlash(filepath.Join("galgame/sessions", id))
}

func (s *GalgameStore) SaveCharacter(c GalgameCharacter) error {
	if !safeGalgameID(c.ID) {
		return fmt.Errorf("invalid character id")
	}
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("character name is required")
	}
	if strings.TrimSpace(c.Description) == "" && strings.TrimSpace(c.Personality) == "" && strings.TrimSpace(c.Scenario) == "" && strings.TrimSpace(c.SystemPrompt) == "" {
		return fmt.Errorf("character definition is required")
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
	if err := s.io.RemoveFile(s.sessionPath(id)); err != nil {
		return err
	}
	return s.io.RemoveAll(s.sessionDir(id))
}
