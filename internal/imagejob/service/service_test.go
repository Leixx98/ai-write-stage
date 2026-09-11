package service

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Leixx98/ai-write-stage/internal/imagejob"
	"github.com/Leixx98/ai-write-stage/internal/store"
)

type fakeProvider struct {
	info  imagejob.ProviderInfo
	err   error
	mu    sync.Mutex
	calls []imagejob.ProviderRequest
}

type blockingProvider struct {
	info    imagejob.ProviderInfo
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockingProvider) Info() imagejob.ProviderInfo { return p.info }
func (p *blockingProvider) ValidateProfile(profile imagejob.Profile) error {
	return imagejob.ValidateProfile(profile, p.info.Capabilities)
}
func (p *blockingProvider) Execute(ctx context.Context, _ imagejob.ProviderRequest, _ Reporter) ([]GeneratedOutput, error) {
	p.once.Do(func() { close(p.started) })
	select {
	case <-p.release:
		return []GeneratedOutput{{Media: imagejob.MediaOutput{Kind: "image", MIME: "image/png"}, Data: []byte("png")}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (p *blockingProvider) Cancel(context.Context, string) error { return nil }
func (p *blockingProvider) TestConnection(context.Context) error { return nil }

func (f *fakeProvider) Info() imagejob.ProviderInfo { return f.info }
func (f *fakeProvider) ValidateProfile(profile imagejob.Profile) error {
	return imagejob.ValidateProfile(profile, f.info.Capabilities)
}
func (f *fakeProvider) Execute(_ context.Context, request imagejob.ProviderRequest, reporter Reporter) ([]GeneratedOutput, error) {
	f.mu.Lock()
	f.calls = append(f.calls, request)
	f.mu.Unlock()
	reporter.ExternalID("external-1")
	reporter.Stage("running", "running")
	if f.err != nil {
		return nil, f.err
	}
	return []GeneratedOutput{{Media: imagejob.MediaOutput{Kind: "image", MIME: "image/png"}, Data: []byte("png")}}, nil
}
func (f *fakeProvider) Cancel(context.Context, string) error { return nil }
func (f *fakeProvider) TestConnection(context.Context) error { return nil }
func (f *fakeProvider) callCount() int                       { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

func newGateway(t *testing.T, providers ...Provider) (*Service, *store.Roots) {
	t.Helper()
	t.Setenv("AINOVEL_HOME", t.TempDir())
	root := t.TempDir()
	roots := store.Open(root, "")
	return New(Config{Root: root, Jobs: roots.Images, Configuration: roots.ImageConfig, Providers: NewRegistry(providers...)}), roots
}

func saveProfile(t *testing.T, roots *store.Roots, id, provider string) {
	t.Helper()
	if err := roots.ImageConfig.SaveProfile(imagejob.Profile{ID: id, Name: id, Provider: provider, ImageCount: 1, TimeoutMS: 5000}); err != nil {
		t.Fatal(err)
	}
}

func waitTerminal(t *testing.T, roots *store.Roots, id string) store.ImageJob {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, err := roots.Images.LoadJob(id)
		if err == nil && TerminalStatus(job.Status) {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish", id)
	return store.ImageJob{}
}

func TestDisabledSceneSkipsWithoutProviderCall(t *testing.T) {
	provider := &fakeProvider{info: imagejob.ProviderInfo{ID: "fake", Name: "Fake", Enabled: true}}
	service, roots := newGateway(t, provider)
	saveProfile(t, roots, "default", "fake")
	job, skipped, err := service.Start(imagejob.SceneImageRequest{Scene: imagejob.SceneNovel, SceneID: "u1", Chapter: 1, Ordinal: 1, Manual: true, ProfileID: "default"})
	if err != nil || !skipped || job.Status != "skipped" {
		t.Fatalf("job=%+v skipped=%v err=%v", job, skipped, err)
	}
	if provider.callCount() != 0 {
		t.Fatal("disabled scene invoked provider")
	}
}

func TestProfileOverrideAndIdempotency(t *testing.T) {
	provider := &fakeProvider{info: imagejob.ProviderInfo{ID: "fake", Name: "Fake", Enabled: true}}
	service, roots := newGateway(t, provider)
	saveProfile(t, roots, "default", "fake")
	saveProfile(t, roots, "override", "fake")
	settings := imagejob.DefaultSettings()
	settings.Chat = imagejob.SceneConfig{Enabled: true, AutoGenerate: true, ChatPolicy: imagejob.ChatEveryReply, EveryN: 3, DefaultProfileID: "default"}
	if err := roots.ImageConfig.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	request := imagejob.SceneImageRequest{Scene: imagejob.SceneChat, SceneID: "session", UnitID: "msg", AssistantIndex: 1, Text: "hello", ProfileID: "override"}
	first, skipped, err := service.Start(request)
	if err != nil || skipped {
		t.Fatalf("start: %v skipped=%v", err, skipped)
	}
	waitTerminal(t, roots, first.JobID)
	second, _, err := service.Start(request)
	if err != nil {
		t.Fatal(err)
	}
	if first.JobID != second.JobID || first.ProfileID != "override" {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if provider.callCount() != 1 {
		t.Fatalf("provider calls=%d", provider.callCount())
	}
}

func TestChatEveryNTriggersAtMultiples(t *testing.T) {
	provider := &fakeProvider{info: imagejob.ProviderInfo{ID: "fake", Name: "Fake", Enabled: true}}
	service, roots := newGateway(t, provider)
	saveProfile(t, roots, "default", "fake")
	settings := imagejob.DefaultSettings()
	settings.Chat = imagejob.SceneConfig{Enabled: true, AutoGenerate: true, ChatPolicy: imagejob.ChatEveryN, EveryN: 3, DefaultProfileID: "default"}
	if err := roots.ImageConfig.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 6; index++ {
		job, skipped, err := service.Start(imagejob.SceneImageRequest{Scene: imagejob.SceneChat, SceneID: "session-" + string(rune('a'+index)), UnitID: "msg", AssistantIndex: index, Text: "reply"})
		if err != nil {
			t.Fatal(err)
		}
		want := index%3 != 0
		if skipped != want {
			t.Fatalf("index=%d skipped=%v want=%v job=%+v", index, skipped, want, job)
		}
		if !skipped {
			waitTerminal(t, roots, job.JobID)
		}
	}
	if provider.callCount() != 2 {
		t.Fatalf("provider calls=%d", provider.callCount())
	}
}

func TestProviderFailureDoesNotFallback(t *testing.T) {
	primary := &fakeProvider{info: imagejob.ProviderInfo{ID: "primary", Name: "Primary", Enabled: true}, err: errors.New("failed")}
	fallback := &fakeProvider{info: imagejob.ProviderInfo{ID: "fallback", Name: "Fallback", Enabled: true}}
	service, roots := newGateway(t, primary, fallback)
	saveProfile(t, roots, "default", "primary")
	settings := imagejob.DefaultSettings()
	settings.Play = imagejob.SceneConfig{Enabled: true, AutoGenerate: true, DefaultProfileID: "default"}
	if err := roots.ImageConfig.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	job, _, err := service.Start(imagejob.SceneImageRequest{Scene: imagejob.ScenePlay, SceneID: "play", Ordinal: 1, Text: "beat"})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitTerminal(t, roots, job.JobID)
	if finished.Status != "failed" || primary.callCount() != 1 || fallback.callCount() != 0 {
		t.Fatalf("job=%+v primary=%d fallback=%d", finished, primary.callCount(), fallback.callCount())
	}
}

func TestDisablingSceneDoesNotCancelRunningJob(t *testing.T) {
	provider := &blockingProvider{info: imagejob.ProviderInfo{ID: "blocking", Name: "Blocking", Enabled: true}, started: make(chan struct{}), release: make(chan struct{})}
	service, roots := newGateway(t, provider)
	saveProfile(t, roots, "default", "blocking")
	settings := imagejob.DefaultSettings()
	settings.Novel = imagejob.SceneConfig{Enabled: true, AutoGenerate: true, DefaultProfileID: "default"}
	if err := roots.ImageConfig.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	job, _, err := service.Start(imagejob.SceneImageRequest{Scene: imagejob.SceneNovel, SceneID: "unit", UnitID: "unit", Chapter: 1, Ordinal: 1, Text: "text"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	settings.Novel.Enabled = false
	if err := roots.ImageConfig.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	close(provider.release)
	if finished := waitTerminal(t, roots, job.JobID); finished.Status != "completed" {
		t.Fatalf("job=%+v", finished)
	}
}

func TestPlayBeatsQueueWhileAnotherIsRunning(t *testing.T) {
	provider := &blockingProvider{info: imagejob.ProviderInfo{ID: "blocking", Name: "Blocking", Enabled: true}, started: make(chan struct{}), release: make(chan struct{})}
	service, roots := newGateway(t, provider)
	saveProfile(t, roots, "default", "blocking")
	settings := imagejob.DefaultSettings()
	settings.Play = imagejob.SceneConfig{Enabled: true, AutoGenerate: true, DefaultProfileID: "default"}
	if err := roots.ImageConfig.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	first, _, err := service.Start(imagejob.SceneImageRequest{Scene: imagejob.ScenePlay, SceneID: "play", Ordinal: 1, Text: "beat-1"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("first play job did not start")
	}
	second, _, err := service.Start(imagejob.SceneImageRequest{Scene: imagejob.ScenePlay, SceneID: "play", Ordinal: 2, Text: "beat-2"})
	if err != nil {
		t.Fatalf("second play beat should queue, got %v", err)
	}
	if second.JobID == "" || second.JobID == first.JobID {
		t.Fatalf("expected a new queued job, first=%q second=%q", first.JobID, second.JobID)
	}
	_, _, err = service.Start(imagejob.SceneImageRequest{Scene: imagejob.ScenePlay, SceneID: "play", Ordinal: 1, Text: "beat-1-retry", Force: true})
	var conflict ConflictError
	if !errors.As(err, &conflict) || conflict.Job.JobID != first.JobID {
		t.Fatalf("same beat should stay busy, err=%v", err)
	}
	close(provider.release)
	if finished := waitTerminal(t, roots, first.JobID); finished.Status != "completed" {
		t.Fatalf("first job=%+v", finished)
	}
	if finished := waitTerminal(t, roots, second.JobID); finished.Status != "completed" {
		t.Fatalf("second job=%+v", finished)
	}
}

func TestChatMessagesQueueWhileAnotherIsRunning(t *testing.T) {
	provider := &blockingProvider{info: imagejob.ProviderInfo{ID: "blocking", Name: "Blocking", Enabled: true}, started: make(chan struct{}), release: make(chan struct{})}
	service, roots := newGateway(t, provider)
	saveProfile(t, roots, "default", "blocking")
	settings := imagejob.DefaultSettings()
	settings.Chat = imagejob.SceneConfig{Enabled: true, AutoGenerate: true, ChatPolicy: imagejob.ChatEveryReply, DefaultProfileID: "default"}
	if err := roots.ImageConfig.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	first, _, err := service.Start(imagejob.SceneImageRequest{Scene: imagejob.SceneChat, SceneID: "session", UnitID: "msg-1", AssistantIndex: 1, Text: "one"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("first chat job did not start")
	}
	second, _, err := service.Start(imagejob.SceneImageRequest{Scene: imagejob.SceneChat, SceneID: "session", UnitID: "msg-2", AssistantIndex: 2, Text: "two"})
	if err != nil {
		t.Fatalf("second chat message should queue, got %v", err)
	}
	if second.JobID == first.JobID {
		t.Fatal("expected a distinct chat job")
	}
	close(provider.release)
	waitTerminal(t, roots, first.JobID)
}

func TestProviderCapabilityRejectsUnsupportedProfileFields(t *testing.T) {
	provider := &fakeProvider{info: imagejob.ProviderInfo{ID: "fake", Name: "Fake", Enabled: true}}
	service, _ := newGateway(t, provider)
	err := service.ValidateProfile(imagejob.Profile{ID: "bad", Name: "Bad", Provider: "fake", AspectRatio: "16:9", ImageCount: 1, TimeoutMS: 5000})
	if err == nil {
		t.Fatal("unsupported aspect ratio was accepted")
	}
}

func TestImagePathUsesSceneIdentity(t *testing.T) {
	root := t.TempDir()
	if got, want := ImagePath(root, store.ImageJob{Scene: imagejob.SceneNovel, Chapter: 3, Ordinal: 2}), filepath.Join(root, store.NovelDirName, "drafts", "03.units", "002.png"); got != want {
		t.Fatalf("novel path=%q want=%q", got, want)
	}
	if got, want := ImagePath(root, store.ImageJob{Scene: imagejob.SceneChat, SceneID: "session", SessionID: "session", JobID: "job"}), filepath.Join(root, store.TavernDirName, "sessions", "session", "images", "job.png"); got != want {
		t.Fatalf("chat path=%q want=%q", got, want)
	}
	if got, want := ImagePath(root, store.ImageJob{Scene: imagejob.ScenePlay, SceneID: "play", PlayID: "play", Ordinal: 2}), filepath.Join(root, store.TavernDirName, "plays", "play", "images", "002.png"); got != want {
		t.Fatalf("play path=%q want=%q", got, want)
	}
}
