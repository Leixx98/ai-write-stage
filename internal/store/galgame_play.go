package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type PlayStatus string

const (
	PlayIdle           PlayStatus = "idle"
	PlayRunning        PlayStatus = "running"
	PlayAwaitingChoice PlayStatus = "awaiting_choice"
	PlayPaused         PlayStatus = "paused"
	PlayCompleted      PlayStatus = "completed"
)

func (s PlayStatus) Active() bool {
	return s == PlayRunning || s == PlayAwaitingChoice
}

type PlayBeatKind string

const (
	BeatNarration PlayBeatKind = "narration"
	BeatDialogue  PlayBeatKind = "dialogue"
	BeatInner     PlayBeatKind = "inner"
	BeatChoice    PlayBeatKind = "choice"
)

type PlayCG string

const (
	PlayCGNew  PlayCG = "new"
	PlayCGKeep PlayCG = "keep"
)

type PlayChoice struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Consequence string `json:"consequence,omitempty"`
}

type PlayChoiceRecord struct {
	Ordinal  int    `json:"ordinal"`
	ChoiceID string `json:"choice_id"`
	Label    string `json:"label,omitempty"`
}

type PlayMeta struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	CharacterID    string     `json:"character_id"`
	Premise        string     `json:"premise"`
	UserPersona    string     `json:"user_persona,omitempty"`
	ImageProfileID string     `json:"image_profile_id,omitempty"`
	Status         PlayStatus `json:"status"`
	LastError      string     `json:"last_error,omitempty"`
	Stage          string     `json:"stage,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type PlayProgress struct {
	PlayHead      int                `json:"play_head"`
	WriteHead     int                `json:"write_head"`
	GateOrdinal   int                `json:"gate_ordinal,omitempty"`
	SegmentID     string             `json:"segment_id,omitempty"`
	ChoiceHistory []PlayChoiceRecord `json:"choice_history,omitempty"`
}

type PlayBeatCard struct {
	Kind          PlayBeatKind `json:"kind"`
	Speaker       string       `json:"speaker,omitempty"`
	Location      string       `json:"location,omitempty"`
	TimeOfDay     string       `json:"time_of_day,omitempty"`
	CG            PlayCG       `json:"cg"`
	CGIntent      string       `json:"cg_intent,omitempty"`
	RequiredBeats []string     `json:"required_beats,omitempty"`
	Choices       []PlayChoice `json:"choices,omitempty"`
}

type PlayOutline struct {
	SegmentID            string         `json:"segment_id"`
	Goal                 string         `json:"goal,omitempty"`
	CompleteAfterSegment bool           `json:"complete_after_segment,omitempty"`
	Notes                string         `json:"notes,omitempty"`
	Cards                []PlayBeatCard `json:"cards"`
	NextCard             int            `json:"next_card"`
}

type PlayBeat struct {
	Ordinal    int          `json:"ordinal"`
	SegmentID  string       `json:"segment_id,omitempty"`
	Kind       PlayBeatKind `json:"kind"`
	Speaker    string       `json:"speaker,omitempty"`
	Text       string       `json:"text"`
	Location   string       `json:"location,omitempty"`
	TimeOfDay  string       `json:"time_of_day,omitempty"`
	CG         PlayCG       `json:"cg"`
	CGIntent   string       `json:"cg_intent,omitempty"`
	ImageJobID string       `json:"image_job_id,omitempty"`
	ImageError string       `json:"image_error,omitempty"`
	Choices    []PlayChoice `json:"choices,omitempty"`
}

type PlayWriterTurn struct {
	Card    PlayBeatCard `json:"card"`
	Speaker string       `json:"speaker,omitempty"`
	Text    string       `json:"text"`
}

type PlayWriterSession struct {
	Turns []PlayWriterTurn `json:"turns"`
}

func (s *GalgameStore) NewPlayID(characterName, playName string, createdAt time.Time) string {
	return s.uniqueFileID(s.playMetaPath, joinGalgameID(createdAt, sanitizeGalgameName(characterName, "character"), sanitizeGalgameName(playName, "play")))
}

func (s *GalgameStore) playDir(id string) string {
	return filepath.ToSlash(filepath.Join("galgame/plays", id))
}
func (s *GalgameStore) playMetaPath(id string) string {
	return filepath.ToSlash(filepath.Join("galgame/plays", id, "meta.json"))
}
func (s *GalgameStore) playProgressPath(id string) string {
	return filepath.ToSlash(filepath.Join("galgame/plays", id, "progress.json"))
}
func (s *GalgameStore) playOutlinePath(id string) string {
	return filepath.ToSlash(filepath.Join("galgame/plays", id, "outline.json"))
}
func (s *GalgameStore) playBeatPath(id string, ordinal int) string {
	return filepath.ToSlash(filepath.Join("galgame/plays", id, "beats", fmt.Sprintf("%03d.json", ordinal)))
}
func (s *GalgameStore) playWriterSessionPath(id string) string {
	return filepath.ToSlash(filepath.Join("galgame/plays", id, "writer_session.json"))
}

func (s *GalgameStore) SavePlay(meta PlayMeta) error {
	if !safeGalgameID(meta.ID) {
		return fmt.Errorf("invalid play id")
	}
	if strings.TrimSpace(meta.Name) == "" {
		return fmt.Errorf("play name is required")
	}
	if strings.TrimSpace(meta.CharacterID) == "" {
		return fmt.Errorf("character_id is required")
	}
	if strings.TrimSpace(meta.Premise) == "" {
		return fmt.Errorf("premise is required")
	}
	if meta.Status == "" {
		meta.Status = PlayIdle
	}
	if !validPlayStatus(meta.Status) {
		return fmt.Errorf("invalid play status %q", meta.Status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = time.Now().UTC()
	}
	meta.UpdatedAt = time.Now().UTC()
	return s.io.WriteJSON(s.playMetaPath(meta.ID), meta)
}

func validPlayStatus(status PlayStatus) bool {
	switch status {
	case PlayIdle, PlayRunning, PlayAwaitingChoice, PlayPaused, PlayCompleted:
		return true
	default:
		return false
	}
}

func (s *GalgameStore) LoadPlay(id string) (PlayMeta, error) {
	if !safeGalgameID(id) {
		return PlayMeta{}, fmt.Errorf("invalid play id")
	}
	var meta PlayMeta
	if err := s.io.ReadJSON(s.playMetaPath(id), &meta); err != nil {
		return PlayMeta{}, err
	}
	return meta, nil
}

func (s *GalgameStore) ListPlays() ([]PlayMeta, error) {
	entries, err := os.ReadDir(filepath.Join(s.io.dir, "galgame/plays"))
	if os.IsNotExist(err) {
		return []PlayMeta{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]PlayMeta, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, loadErr := s.LoadPlay(entry.Name())
		if loadErr != nil {
			continue
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s *GalgameStore) ActivePlay() (PlayMeta, bool, error) {
	plays, err := s.ListPlays()
	if err != nil {
		return PlayMeta{}, false, err
	}
	for _, play := range plays {
		if play.Status.Active() {
			return play, true, nil
		}
	}
	return PlayMeta{}, false, nil
}

func (s *GalgameStore) DeletePlay(id string) error {
	if !safeGalgameID(id) {
		return fmt.Errorf("invalid play id")
	}
	return s.io.RemoveAll(s.playDir(id))
}

func (s *GalgameStore) SaveProgress(id string, progress PlayProgress) error {
	if !safeGalgameID(id) {
		return fmt.Errorf("invalid play id")
	}
	if progress.PlayHead < 0 || progress.WriteHead < 0 {
		return fmt.Errorf("play heads cannot be negative")
	}
	if progress.PlayHead > progress.WriteHead {
		return fmt.Errorf("play_head cannot exceed write_head")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.io.WriteJSON(s.playProgressPath(id), progress)
}

func (s *GalgameStore) LoadProgress(id string) (PlayProgress, error) {
	if !safeGalgameID(id) {
		return PlayProgress{}, fmt.Errorf("invalid play id")
	}
	var progress PlayProgress
	err := s.io.ReadJSON(s.playProgressPath(id), &progress)
	if os.IsNotExist(err) {
		return PlayProgress{}, nil
	}
	return progress, err
}

func (s *GalgameStore) SaveOutline(id string, outline PlayOutline) error {
	if !safeGalgameID(id) {
		return fmt.Errorf("invalid play id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.io.WriteJSON(s.playOutlinePath(id), outline)
}

func (s *GalgameStore) LoadOutline(id string) (PlayOutline, error) {
	if !safeGalgameID(id) {
		return PlayOutline{}, fmt.Errorf("invalid play id")
	}
	var outline PlayOutline
	err := s.io.ReadJSON(s.playOutlinePath(id), &outline)
	if os.IsNotExist(err) {
		return PlayOutline{}, nil
	}
	return outline, err
}

func (s *GalgameStore) SaveBeat(id string, beat PlayBeat) error {
	if !safeGalgameID(id) {
		return fmt.Errorf("invalid play id")
	}
	if beat.Ordinal < 1 {
		return fmt.Errorf("beat ordinal must be >= 1")
	}
	if strings.TrimSpace(beat.Text) == "" {
		return fmt.Errorf("beat text is required")
	}
	if beat.CG == "" {
		beat.CG = PlayCGKeep
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.io.WriteJSON(s.playBeatPath(id, beat.Ordinal), beat)
}

func (s *GalgameStore) LoadBeat(id string, ordinal int) (PlayBeat, error) {
	if !safeGalgameID(id) {
		return PlayBeat{}, fmt.Errorf("invalid play id")
	}
	if ordinal < 1 {
		return PlayBeat{}, fmt.Errorf("beat ordinal must be >= 1")
	}
	var beat PlayBeat
	err := s.io.ReadJSON(s.playBeatPath(id, ordinal), &beat)
	return beat, err
}

func (s *GalgameStore) ListBeats(id string) ([]PlayBeat, error) {
	if !safeGalgameID(id) {
		return nil, fmt.Errorf("invalid play id")
	}
	dir := filepath.Join(s.io.dir, "galgame", "plays", id, "beats")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []PlayBeat{}, nil
	}
	if err != nil {
		return nil, err
	}
	ordinals := make([]int, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		n, parseErr := strconv.Atoi(strings.TrimSuffix(entry.Name(), ".json"))
		if parseErr != nil || n < 1 {
			continue
		}
		ordinals = append(ordinals, n)
	}
	sort.Ints(ordinals)
	out := make([]PlayBeat, 0, len(ordinals))
	for _, ordinal := range ordinals {
		beat, loadErr := s.LoadBeat(id, ordinal)
		if loadErr != nil {
			return nil, loadErr
		}
		out = append(out, beat)
	}
	return out, nil
}

func (s *GalgameStore) ListBeatsFrom(id string, from int) ([]PlayBeat, error) {
	beats, err := s.ListBeats(id)
	if err != nil {
		return nil, err
	}
	if from <= 1 {
		return beats, nil
	}
	out := make([]PlayBeat, 0, len(beats))
	for _, beat := range beats {
		if beat.Ordinal >= from {
			out = append(out, beat)
		}
	}
	return out, nil
}

func (s *GalgameStore) SaveWriterSession(id string, session PlayWriterSession) error {
	if !safeGalgameID(id) {
		return fmt.Errorf("invalid play id")
	}
	if session.Turns == nil {
		session.Turns = []PlayWriterTurn{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.io.WriteJSON(s.playWriterSessionPath(id), session)
}

func (s *GalgameStore) LoadWriterSession(id string) (PlayWriterSession, error) {
	if !safeGalgameID(id) {
		return PlayWriterSession{}, fmt.Errorf("invalid play id")
	}
	var session PlayWriterSession
	err := s.io.ReadJSON(s.playWriterSessionPath(id), &session)
	if os.IsNotExist(err) {
		return PlayWriterSession{}, nil
	}
	if err != nil {
		return PlayWriterSession{}, err
	}
	if session.Turns == nil {
		session.Turns = []PlayWriterTurn{}
	}
	return session, nil
}

type BoundPlayImage struct {
	JobID   string
	Ordinal int
	Error   string
}

func DisplayBoundImage(beats []PlayBeat, ordinal int) BoundPlayImage {
	for i := len(beats) - 1; i >= 0; i-- {
		beat := beats[i]
		if beat.Ordinal > ordinal || beat.CG != PlayCGNew {
			continue
		}
		return BoundPlayImage{JobID: strings.TrimSpace(beat.ImageJobID), Ordinal: beat.Ordinal, Error: strings.TrimSpace(beat.ImageError)}
	}
	return BoundPlayImage{}
}

func DisplayImageJobID(beats []PlayBeat, ordinal int) string {
	return DisplayBoundImage(beats, ordinal).JobID
}
