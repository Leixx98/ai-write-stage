package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func seededExportController(t *testing.T) *v2Controller {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("导出测试", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "开端"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "第一段。"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 3, "", ""); err != nil {
		t.Fatal(err)
	}
	return &v2Controller{st: st}
}

func TestExportBookTXTReturnsPlainText(t *testing.T) {
	c := seededExportController(t)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/export", bytes.NewBufferString(`{"format":"txt"}`))
	c.exportBook(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("content-type = %q", recorder.Header().Get("Content-Type"))
	}
	if !strings.Contains(recorder.Body.String(), "第一段。") {
		t.Fatalf("txt export missing chapter text: %s", recorder.Body.String())
	}
}

func TestExportBookRejectsUnknownFormat(t *testing.T) {
	c := seededExportController(t)
	recorder := httptest.NewRecorder()
	c.exportBook(recorder, httptest.NewRequest(http.MethodPost, "/api/v2/export", bytes.NewBufferString(`{"format":"pdf"}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestExportBookDefaultsToEPUB(t *testing.T) {
	c := seededExportController(t)
	recorder := httptest.NewRecorder()
	c.exportBook(recorder, httptest.NewRequest(http.MethodPost, "/api/v2/export", bytes.NewBufferString(`{}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Type") != "application/epub+zip" {
		t.Fatalf("content-type = %q", recorder.Header().Get("Content-Type"))
	}
	if !bytes.HasPrefix(recorder.Body.Bytes(), []byte("PK")) {
		t.Fatal("default export is not an EPUB zip")
	}
}
