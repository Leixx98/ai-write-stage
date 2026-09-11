package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenUsesNovelAndTavernRoots(t *testing.T) {
	workspace := t.TempDir()
	roots := Open(workspace, workspace)
	if roots.Workspace != workspace {
		t.Fatalf("Workspace = %q", roots.Workspace)
	}
	if roots.Facts.Dir() != NovelDir(workspace) {
		t.Fatalf("Facts.Dir = %q", roots.Facts.Dir())
	}
	if err := roots.Facts.Init(); err != nil {
		t.Fatal(err)
	}
	if err := roots.Facts.Outline.SavePremise("一句话开书"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, NovelDirName, "premise.md")); err != nil {
		t.Fatalf("novel premise: %v", err)
	}
	if err := roots.Tavern.SaveCharacter(GalgameCharacter{ID: "linwan", Name: "林晚", Description: "情报员"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, TavernDirName, "characters", "linwan.json")); err != nil {
		t.Fatalf("tavern character: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "galgame")); !os.IsNotExist(err) {
		t.Fatal("galgame must not be created")
	}
}

func TestNewStoreArgumentIsWorkspaceRoot(t *testing.T) {
	workspace := t.TempDir()
	facts := NewStore(workspace)
	if got, want := facts.Dir(), NovelDir(workspace); got != want {
		t.Fatalf("NewStore(%q).Dir = %q, want %q", workspace, got, want)
	}
}
