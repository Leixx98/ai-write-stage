package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeleteSessionRemovesImages(t *testing.T) {
	dir := t.TempDir()
	tavern := Open(dir, dir).Tavern
	session := GalgameSession{ID: "session_1", Name: "s", CharacterID: "c"}
	if err := tavern.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(dir, "galgame", "sessions", "session_1", "images", "img_1.png")
	if err := os.MkdirAll(filepath.Dir(img), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(img, []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := tavern.DeleteSession("session_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "galgame", "sessions", "session_1.json")); !os.IsNotExist(err) {
		t.Fatalf("session json still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "galgame", "sessions", "session_1")); !os.IsNotExist(err) {
		t.Fatalf("session images dir still exists: %v", err)
	}
}

func TestNewGalgameIDsUseReadableNames(t *testing.T) {
	tavern := Open(t.TempDir(), t.TempDir()).Tavern
	created := time.Date(2026, 8, 17, 7, 53, 12, 0, time.UTC)
	got := tavern.NewCharacterID("林晚", created)
	if got != "林晚_20260817_075312" {
		t.Fatalf("character id = %q", got)
	}
	got = tavern.NewSessionID("林晚", "林晚 会话", created)
	if got != "林晚_林晚_会话_20260817_075312" {
		t.Fatalf("session id = %q", got)
	}
	got = tavern.NewCharacterID(`<>:"/\|?*  name`, created)
	if got != "name_20260817_075312" {
		t.Fatalf("sanitized id = %q", got)
	}
}

func TestNewGalgameIDsAvoidCollisions(t *testing.T) {
	dir := t.TempDir()
	tavern := Open(dir, dir).Tavern
	created := time.Date(2026, 8, 17, 7, 53, 12, 0, time.UTC)
	first := tavern.NewCharacterID("林晚", created)
	if err := tavern.SaveCharacter(GalgameCharacter{ID: first, Name: "林晚", Description: "角色"}); err != nil {
		t.Fatal(err)
	}
	second := tavern.NewCharacterID("林晚", created)
	if second != first+"_2" {
		t.Fatalf("collision id = %q, want %q", second, first+"_2")
	}
	if strings.ContainsAny(second, `<>:"/\|?*`) {
		t.Fatalf("unsafe id %q", second)
	}
}
