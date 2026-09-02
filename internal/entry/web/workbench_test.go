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
	for _, id := range []string{"pause", "action-other", "action-replan", "action-rewrite", "action-modal", "export-book", "reader-open", "reopen-open"} {
		if !bytes.Contains(data, []byte(`id="`+id+`"`)) {
			t.Fatalf("missing %s", id)
		}
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
