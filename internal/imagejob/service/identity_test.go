package service

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/imagejob"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestStartUnitRejectsMissingChapterOrdinal(t *testing.T) {
	svc := New(Config{})
	_, err := svc.StartUnit(0, 1, false, PromptRun{})
	if !errors.Is(err, ErrInvalidUnitIdentity) {
		t.Fatalf("chapter=0: %v", err)
	}
	_, err = svc.StartUnit(1, 0, false, PromptRun{})
	if !errors.Is(err, ErrInvalidUnitIdentity) {
		t.Fatalf("ordinal=0: %v", err)
	}
}

func TestStartGalgameRejectsChapterOrdinal(t *testing.T) {
	svc := New(Config{})
	_, err := svc.StartGalgame("session_1", PromptRun{Request: imagejob.PromptRequest{Chapter: 1}})
	if !errors.Is(err, ErrInvalidGalgameIdentity) {
		t.Fatalf("chapter: %v", err)
	}
	_, err = svc.StartGalgame("session_1", PromptRun{Request: imagejob.PromptRequest{Ordinal: 2}})
	if !errors.Is(err, ErrInvalidGalgameIdentity) {
		t.Fatalf("ordinal: %v", err)
	}
}

func TestStartGalgameRejectsMissingSession(t *testing.T) {
	svc := New(Config{})
	_, err := svc.StartGalgame("", PromptRun{})
	if !errors.Is(err, ErrInvalidGalgameIdentity) {
		t.Fatalf("empty session: %v", err)
	}
	_, err = svc.StartGalgame("../escape", PromptRun{})
	if !errors.Is(err, ErrInvalidGalgameIdentity) {
		t.Fatalf("unsafe session: %v", err)
	}
}

func TestImagePathKeepsGalgameOffDrafts(t *testing.T) {
	root := t.TempDir()
	galgame := store.ImageJob{JobID: "job_gal", Trigger: TriggerGalgame, SessionID: "session_1", Chapter: 3, Ordinal: 2}
	got := ImagePath(root, galgame)
	want := filepath.Join(root, "galgame", "sessions", "session_1", "images", "job_gal.png")
	if got != want {
		t.Fatalf("galgame path = %q, want %q", got, want)
	}
	if strings.Contains(got, "drafts") || strings.Contains(got, "tests") {
		t.Fatal("galgame image must not use drafts or tests")
	}
	legacy := store.ImageJob{JobID: "job_old", Trigger: TriggerGalgame}
	got = ImagePath(root, legacy)
	want = filepath.Join(root, "meta", "images", "tests", "job_old.png")
	if got != want {
		t.Fatalf("legacy galgame path = %q, want %q", got, want)
	}
	unit := store.ImageJob{JobID: "job_unit", Trigger: TriggerUnit, Chapter: 3, Ordinal: 2}
	got = ImagePath(root, unit)
	want = filepath.Join(root, "drafts", "03.units", "002.png")
	if got != want {
		t.Fatalf("unit path = %q, want %q", got, want)
	}
}

func TestGalgameIdempotencyKeyIsNamespaced(t *testing.T) {
	req := imagejob.PromptRequest{UnitID: "msg_1", UnitText: "hello"}
	unitKey := UnitIdempotencyKey(req, "wf", "wh", "sh", "pf")
	galKey := GalgameIdempotencyKey("session_1", req, "wf", "wh", "sh", "pf")
	other := GalgameIdempotencyKey("session_2", req, "wf", "wh", "sh", "pf")
	if !strings.HasPrefix(galKey, "galgame:session_1:") {
		t.Fatalf("galgame key = %q", galKey)
	}
	if galKey == unitKey {
		t.Fatal("galgame and unit keys must differ")
	}
	if galKey == other {
		t.Fatal("galgame keys must include session id")
	}
}

func TestIsUnitJobIgnoresGalgame(t *testing.T) {
	if IsUnitJob(store.ImageJob{Trigger: TriggerGalgame, Chapter: 1, Ordinal: 1}) {
		t.Fatal("galgame job must not count as unit")
	}
	if !IsUnitJob(store.ImageJob{Trigger: TriggerUnit, Chapter: 1, Ordinal: 1}) {
		t.Fatal("unit trigger should match")
	}
	if !IsUnitJob(store.ImageJob{Chapter: 1, Ordinal: 1}) {
		t.Fatal("legacy job with chapter/ordinal should match")
	}
}
