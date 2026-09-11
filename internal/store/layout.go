package store

import "path/filepath"

const (
	NovelDirName  = "novel"
	TavernDirName = "tavern"
)

func NovelDir(workspace string) string {
	return filepath.Join(workspace, NovelDirName)
}

func TavernDir(workspace string) string {
	return filepath.Join(workspace, TavernDirName)
}
