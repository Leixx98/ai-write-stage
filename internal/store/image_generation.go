package store

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Leixx98/ai-write-stage/internal/imagejob"
)

type ImageJob struct {
	JobID            string                     `json:"job_id"`
	Scene            imagejob.Scene             `json:"scene"`
	SceneID          string                     `json:"scene_id"`
	UnitID           string                     `json:"unit_id,omitempty"`
	SessionID        string                     `json:"session_id,omitempty"`
	PlayID           string                     `json:"play_id,omitempty"`
	Chapter          int                        `json:"chapter,omitempty"`
	Ordinal          int                        `json:"ordinal,omitempty"`
	ProfileID        string                     `json:"profile_id"`
	Provider         string                     `json:"provider"`
	Status           string                     `json:"status"`
	Stage            string                     `json:"stage,omitempty"`
	ExternalJobID    string                     `json:"external_job_id,omitempty"`
	Attempt          int                        `json:"attempt"`
	IdempotencyKey   string                     `json:"idempotency_key,omitempty"`
	Trigger          string                     `json:"trigger,omitempty"`
	RequestSnapshot  imagejob.SceneImageRequest `json:"request_snapshot"`
	ProviderSnapshot json.RawMessage            `json:"provider_snapshot,omitempty"`
	PromptRaw        string                     `json:"prompt_raw,omitempty"`
	PromptValues     map[string]any             `json:"prompt_values,omitempty"`
	SnapshotKey      string                     `json:"snapshot_key,omitempty"`
	Recoverable      bool                       `json:"recoverable,omitempty"`
	Output           map[string]any             `json:"output,omitempty"`
	Outputs          []imagejob.MediaOutput     `json:"outputs,omitempty"`
	Error            string                     `json:"error,omitempty"`
	StartedAt        time.Time                  `json:"started_at,omitempty"`
	FinishedAt       *time.Time                 `json:"finished_at,omitempty"`
	ProgressCurrent  int                        `json:"progress_current,omitempty"`
	ProgressTotal    int                        `json:"progress_total,omitempty"`
	ProgressNode     string                     `json:"progress_node,omitempty"`
}

type ImageStore struct {
	io *IO
}

func NewImageStore(io *IO) *ImageStore { return &ImageStore{io: io} }

func (s *ImageStore) jobPath(id string) string {
	return filepath.ToSlash(filepath.Join("meta/image-generation/jobs", id+".json"))
}

func (s *ImageStore) SaveJob(job ImageJob) error {
	if !safeImageID(job.JobID) {
		return errors.New("job_id is required")
	}
	return s.io.WriteJSON(s.jobPath(job.JobID), job)
}

func (s *ImageStore) LoadJob(id string) (ImageJob, error) {
	if !safeImageID(id) {
		return ImageJob{}, fmt.Errorf("invalid job id")
	}
	var job ImageJob
	err := s.io.ReadJSON(s.jobPath(id), &job)
	return job, err
}

func (s *ImageStore) ListJobs() ([]ImageJob, error) {
	dir := filepath.Join(s.io.dir, "meta", "image-generation", "jobs")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []ImageJob{}, nil
	}
	if err != nil {
		return nil, err
	}
	jobs := make([]ImageJob, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" || strings.HasSuffix(entry.Name(), ".provider.json") {
			continue
		}
		var job ImageJob
		if err := s.io.ReadJSON(filepath.ToSlash(filepath.Join("meta/image-generation/jobs", entry.Name())), &job); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].StartedAt.Before(jobs[j].StartedAt) })
	return jobs, nil
}

func (s *ImageStore) FindJobByIdempotencyKey(key string) (ImageJob, bool, error) {
	if strings.TrimSpace(key) == "" {
		return ImageJob{}, false, nil
	}
	jobs, err := s.ListJobs()
	if err != nil {
		return ImageJob{}, false, err
	}
	for index := len(jobs) - 1; index >= 0; index-- {
		if jobs[index].IdempotencyKey == key {
			return jobs[index], true, nil
		}
	}
	return ImageJob{}, false, nil
}

func (s *ImageStore) SaveProviderSnapshot(jobID string, snapshot any) (string, error) {
	if !safeImageID(jobID) {
		return "", fmt.Errorf("invalid job id")
	}
	key := filepath.ToSlash(filepath.Join("meta/image-generation/jobs", jobID+".provider.json"))
	if err := s.io.WriteJSON(key, snapshot); err != nil {
		return "", err
	}
	return key, nil
}

func (s *ImageStore) LoadProviderSnapshot(jobID string, value any) error {
	if !safeImageID(jobID) {
		return fmt.Errorf("invalid job id")
	}
	return s.io.ReadJSON(filepath.ToSlash(filepath.Join("meta/image-generation/jobs", jobID+".provider.json")), value)
}

func (s *ImageStore) SaveMedia(data []byte, name, mime string) (imagejob.MediaRef, error) {
	sum := sha256.Sum256(data)
	hash := fmt.Sprintf("%x", sum[:])
	ext := strings.ToLower(filepath.Ext(name))
	if len(ext) > 10 {
		ext = ""
	}
	key := filepath.ToSlash(filepath.Join("assets/input", hash+ext))
	if err := s.io.WriteFileUnlocked(key, data); err != nil {
		return imagejob.MediaRef{}, err
	}
	ref := imagejob.MediaRef{ID: hash, SHA256: hash, Source: "local", StorageKey: key, UploadName: filepath.Base(name), MIME: mime, Size: int64(len(data))}
	if err := s.io.WriteJSON(filepath.ToSlash(filepath.Join("meta/image-generation/media", hash+".json")), ref); err != nil {
		return imagejob.MediaRef{}, err
	}
	return ref, nil
}

func (s *ImageStore) LoadMedia(id string) (imagejob.MediaRef, string, error) {
	if !safeImageID(id) {
		return imagejob.MediaRef{}, "", fmt.Errorf("invalid media id")
	}
	var ref imagejob.MediaRef
	if err := s.io.ReadJSON(filepath.ToSlash(filepath.Join("meta/image-generation/media", id+".json")), &ref); err != nil {
		return ref, "", err
	}
	clean := filepath.ToSlash(filepath.Clean(ref.StorageKey))
	if !strings.HasPrefix(clean, "assets/input/") || strings.Contains(clean, "..") {
		return imagejob.MediaRef{}, "", fmt.Errorf("invalid media storage key")
	}
	ref.StorageKey = clean
	return ref, filepath.Join(s.io.dir, filepath.FromSlash(clean)), nil
}

func NewImageJobID() string { return fmt.Sprintf("img_%d", time.Now().UnixNano()) }

type ImageConfigStore struct {
	io *IO
}

func NewImageConfigStore(io *IO) *ImageConfigStore { return &ImageConfigStore{io: io} }

func (s *ImageConfigStore) LoadSettings() (imagejob.Settings, error) {
	settings := imagejob.DefaultSettings()
	err := s.io.ReadJSON("settings.json", &settings)
	if os.IsNotExist(err) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	settings = imagejob.NormalizeSettings(settings)
	return settings, imagejob.ValidateSettings(settings)
}

func (s *ImageConfigStore) SaveSettings(settings imagejob.Settings) error {
	settings = imagejob.NormalizeSettings(settings)
	if err := imagejob.ValidateSettings(settings); err != nil {
		return err
	}
	return s.io.WriteJSON("settings.json", settings)
}

func (s *ImageConfigStore) LoadProfiles() ([]imagejob.Profile, error) {
	var doc struct {
		Version  int                `json:"version"`
		Profiles []imagejob.Profile `json:"profiles"`
	}
	err := s.io.ReadJSON("profiles.json", &doc)
	if os.IsNotExist(err) {
		return []imagejob.Profile{}, nil
	}
	if err != nil {
		return nil, err
	}
	for index := range doc.Profiles {
		doc.Profiles[index] = imagejob.NormalizeProfile(doc.Profiles[index])
	}
	sort.Slice(doc.Profiles, func(i, j int) bool { return doc.Profiles[i].ID < doc.Profiles[j].ID })
	return doc.Profiles, nil
}

func (s *ImageConfigStore) SaveProfiles(profiles []imagejob.Profile) error {
	seen := map[string]bool{}
	for index := range profiles {
		profiles[index] = imagejob.NormalizeProfile(profiles[index])
		if !safeImageID(profiles[index].ID) || seen[profiles[index].ID] {
			return fmt.Errorf("profile id is invalid or duplicated")
		}
		seen[profiles[index].ID] = true
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	return s.io.WriteJSON("profiles.json", struct {
		Version  int                `json:"version"`
		Profiles []imagejob.Profile `json:"profiles"`
	}{Version: 1, Profiles: profiles})
}

func (s *ImageConfigStore) LoadProfile(id string) (imagejob.Profile, error) {
	profiles, err := s.LoadProfiles()
	if err != nil {
		return imagejob.Profile{}, err
	}
	for _, profile := range profiles {
		if profile.ID == id {
			return profile, nil
		}
	}
	return imagejob.Profile{}, os.ErrNotExist
}

func (s *ImageConfigStore) SaveProfile(profile imagejob.Profile) error {
	profiles, err := s.LoadProfiles()
	if err != nil {
		return err
	}
	profile = imagejob.NormalizeProfile(profile)
	found := false
	for index := range profiles {
		if profiles[index].ID == profile.ID {
			profiles[index] = profile
			found = true
			break
		}
	}
	if !found {
		profiles = append(profiles, profile)
	}
	return s.SaveProfiles(profiles)
}

func (s *ImageConfigStore) DeleteProfile(id string) error {
	profiles, err := s.LoadProfiles()
	if err != nil {
		return err
	}
	filtered := profiles[:0]
	for _, profile := range profiles {
		if profile.ID != id {
			filtered = append(filtered, profile)
		}
	}
	return s.SaveProfiles(filtered)
}

func (s *ImageConfigStore) LoadPrompterPresets() (imagejob.PrompterPresetDocument, error) {
	doc := imagejob.PrompterPresetDocument{Version: 1, Presets: map[string]imagejob.PrompterPreset{}}
	err := s.io.ReadJSON("prompter-presets.json", &doc)
	if os.IsNotExist(err) {
		return doc, nil
	}
	if err != nil {
		return doc, err
	}
	if doc.Presets == nil {
		doc.Presets = map[string]imagejob.PrompterPreset{}
	}
	return doc, nil
}

func (s *ImageConfigStore) SavePrompterPresets(doc imagejob.PrompterPresetDocument) error {
	doc.Version = 1
	if doc.Presets == nil {
		doc.Presets = map[string]imagejob.PrompterPreset{}
	}
	return s.io.WriteJSON("prompter-presets.json", doc)
}

func safeImageID(id string) bool {
	return id != "" && filepath.Base(id) == id && !strings.ContainsAny(id, `/\\`) && id != "." && id != ".."
}
