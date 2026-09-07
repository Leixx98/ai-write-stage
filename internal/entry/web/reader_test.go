package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Leixx98/ai-write-stage/internal/domain"
	"github.com/Leixx98/ai-write-stage/internal/store"
)

func seededReaderController(t *testing.T) *v2Controller {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("阅读测试", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "正式章节"},
		{Chapter: 2, Title: "仍在写作"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "第一段。\n\n![插图](../drafts/01.units/001.png)\n\n<script>alert(1)</script>"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 3, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(2, "尚未正式提交。"); err != nil {
		t.Fatal(err)
	}
	return &v2Controller{st: st}
}

func TestReaderListsOnlyFormalChapters(t *testing.T) {
	c := seededReaderController(t)
	recorder := httptest.NewRecorder()
	c.readerChapters(recorder, httptest.NewRequest(http.MethodGet, "/api/v2/chapters", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			NovelName string          `json:"novel_name"`
			Chapters  []readerChapter `json:"chapters"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.NovelName != "阅读测试" || len(response.Data.Chapters) != 1 {
		t.Fatalf("unexpected reader chapters: %+v", response.Data)
	}
	if response.Data.Chapters[0].Chapter != 1 || response.Data.Chapters[0].Title != "正式章节" {
		t.Fatalf("unexpected formal chapter: %+v", response.Data.Chapters[0])
	}
}

func TestReaderRendersSafeMarkdownAndRewritesUnitImages(t *testing.T) {
	c := seededReaderController(t)
	recorder := httptest.NewRecorder()
	c.readerChapter(recorder, httptest.NewRequest(http.MethodGet, "/api/v2/chapters/1", nil), "1")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data readerChapterDocument `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	body := response.Data.HTML
	for _, want := range []string{"<p>第一段。</p>", "/api/v2/units/1/1/image"} {
		if !strings.Contains(body, want) {
			t.Fatalf("reader response missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, "../drafts/") {
		t.Fatalf("unsafe or local-only content leaked into reader response: %s", body)
	}
}

func TestReaderRejectsUncommittedChapter(t *testing.T) {
	c := seededReaderController(t)
	recorder := httptest.NewRecorder()
	c.readerChapter(recorder, httptest.NewRequest(http.MethodGet, "/api/v2/chapters/2", nil), "2")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
