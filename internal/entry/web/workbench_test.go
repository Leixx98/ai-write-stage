package web

import (
	"bytes"
	"testing"
)

func TestNovelWorkbenchUsesActionButtons(t *testing.T) {
	data, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`id="prompt"`)) || bytes.Contains(data, []byte(`id="send"`)) {
		t.Fatal("novel page still has the command input")
	}
	for _, id := range []string{"pause", "action-other", "action-replan", "action-rewrite", "action-modal", "export-open", "export-options", "reader-open", "reopen-open", "welcome-workspace-list", "welcome-workspace-create", "workspace-switcher", "workspace-current"} {
		if !bytes.Contains(data, []byte(`id="`+id+`"`)) {
			t.Fatalf("missing %s", id)
		}
	}
	if bytes.Contains(data, []byte(`id="export-txt"`)) || bytes.Contains(data, []byte(`id="export-book"`)) {
		t.Fatal("export should be a single menu button")
	}
}

func TestExportMenuOpensAboveButton(t *testing.T) {
	script, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"closeExportMenu", "toggleExportMenu", "data-export", "openWorkspace", "createAndOpenWorkspace", "Galgame?.resetForWorkspace", "GalgamePlay?.resetForWorkspace", "window.Galgame?.load()"} {
		if !bytes.Contains(script, []byte(needle)) {
			t.Fatalf("app.js missing %s", needle)
		}
	}
	css, err := staticFiles.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(css, []byte(".export-options")) || !bytes.Contains(css, []byte("bottom:calc(100% + 6px)")) || !bytes.Contains(css, []byte(".workspace-switcher")) || !bytes.Contains(css, []byte(".welcome-workspace-list")) {
		t.Fatal("export menu should open above the button")
	}
}

func TestNotifyUsesPopupToast(t *testing.T) {
	script, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(script, []byte("toast-pop")) || !bytes.Contains(script, []byte("const toast = $('toast')")) {
		t.Fatal("notify should render a popup toast")
	}
	css, err := staticFiles.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(css, []byte(".toast.success")) || !bytes.Contains(css, []byte("@keyframes toast-pop")) {
		t.Fatal("toast popup styles missing")
	}
}

func TestGalgameHidesImagePaneWhenSceneDisabled(t *testing.T) {
	css, err := staticFiles.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`body[data-image-chat-enabled="false"] .galgame-stage:not(.is-play) .galgame-image { display:none; }`,
		`body[data-image-play-enabled="false"] .galgame-stage.is-play .galgame-image { display:none; }`,
		`body[data-image-chat-enabled="false"] .galgame-stage:not(.is-play) { grid-template-columns:minmax(0,1fr);`,
		`body[data-image-play-enabled="false"] .galgame-stage.is-play .galgame-dialogue-slot { max-height:none; }`,
	} {
		if !bytes.Contains(css, []byte(needle)) {
			t.Fatalf("style.css missing tavern image-off layout: %s", needle)
		}
	}
	script, err := staticFiles.ReadFile("static/play.js")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(script, []byte("ImageGeneration?.applyVisibility")) {
		t.Fatal("play mode switch should refresh image visibility")
	}
	if !bytes.Contains(script, []byte("resetForWorkspace")) {
		t.Fatal("play.js should reset theater state when the workspace changes")
	}
	galgame, err := staticFiles.ReadFile("static/galgame.js")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(galgame, []byte("resetForWorkspace")) || !bytes.Contains(galgame, []byte("if (galgameState.loaded) return")) {
		t.Fatal("galgame.js should expose a workspace reset that clears the loaded cache")
	}
}

func TestPlaySettingsExposeDensity(t *testing.T) {
	html, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{`id="galgame-play-density"`, `精简（适合小模型）`, `丰满（适合大模型）`, `id="galgame-play-pacing"`, `选择多（3–6 拍出选项）`, `剧情多（10–15 拍出选项）`, `纯剧情（不出选项）`} {
		if !bytes.Contains(html, []byte(needle)) {
			t.Fatalf("index.html missing %s", needle)
		}
	}
	script, err := staticFiles.ReadFile("static/play.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{`galgame-play-density`, `density: fieldValue('galgame-play-density')`, `galgame-play-pacing`, `pacing: fieldValue('galgame-play-pacing')`} {
		if !bytes.Contains(script, []byte(needle)) {
			t.Fatalf("play.js missing %s", needle)
		}
	}
}

func TestNovelOutlineRowsExpandInPlace(t *testing.T) {
	script, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{`chapter-open`, `data-chapter`, `expandedChapters`} {
		if !bytes.Contains(script, []byte(needle)) {
			t.Fatalf("app.js missing %s", needle)
		}
	}
	css, err := staticFiles.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(css, []byte(`.chapter-row.chapter-open small`)) {
		t.Fatal("style.css missing expanded outline rules")
	}
}
