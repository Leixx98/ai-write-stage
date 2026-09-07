package host

import (
	"testing"

	"github.com/Leixx98/ai-write-stage/internal/domain"
	storepkg "github.com/Leixx98/ai-write-stage/internal/store"
)

func TestCurrentUnitOrdinalUsesCompletedWritingUnits(t *testing.T) {
	var st *storepkg.Store = storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	var plan domain.ChapterPlan = domain.ChapterPlan{Chapter: 3, Scenes: []domain.ScenePlan{{ID: "scene-1", Units: []domain.WritingUnit{{ID: "unit-1"}, {ID: "unit-2"}}}}}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if got := currentUnitOrdinal(st.Drafts, 3); got != 0 {
		t.Fatalf("current unit before writing = %d, want 0", got)
	}
	if _, _, err := st.Drafts.SaveWritingUnit(3, 1, 2, 1, "first unit"); err != nil {
		t.Fatal(err)
	}
	if got := currentUnitOrdinal(st.Drafts, 3); got != 1 {
		t.Fatalf("current unit after first unit = %d, want 1", got)
	}
}
