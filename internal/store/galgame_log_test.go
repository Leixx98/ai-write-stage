package store

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func withTestLogCap(t *testing.T, cap int64) {
	t.Helper()
	prev := tavernLogCapBytes
	tavernLogCapBytes = cap
	t.Cleanup(func() { tavernLogCapBytes = prev })
}

func TestTavernLogRotationKeepsActiveFileBounded(t *testing.T) {
	withTestLogCap(t, 120)
	s := NewGalgameStore(newIO(t.TempDir()))
	line := strings.Repeat("a", 50)
	rel := "plays/p1/runtime.log"
	for i := 0; i < 6; i++ {
		if err := s.AppendText(rel, line); err != nil {
			t.Fatal(err)
		}
	}
	active, err := s.ReadText(rel)
	if err != nil {
		t.Fatal(err)
	}
	if got := int64(len(active)); got > tavernLogCapBytes+int64(len(line))+1 {
		t.Fatalf("active file not bounded: %d bytes", got)
	}
	if int64(len(active)) >= int64(6*(len(line)+1)) {
		t.Fatal("rotation did not drop old content")
	}
	old, err := s.ReadText(rel + ".old")
	if err != nil || old == "" {
		t.Fatalf("rotated archive missing: %q %v", old, err)
	}
	if !strings.Contains(old, line) {
		t.Fatal("rotated archive lost content")
	}
}

func TestTavernLogObserverSeesOffsetsAndEpochs(t *testing.T) {
	withTestLogCap(t, 64)
	s := NewGalgameStore(newIO(t.TempDir()))
	type entry struct {
		rel   string
		epoch int
		off   int64
		data  string
	}
	var got []entry
	s.SetLogObserver(func(rel string, epoch int, off int64, data []byte) {
		got = append(got, entry{rel, epoch, off, string(data)})
	})
	rel := "plays/p1/runtime.log"
	appends := []string{strings.Repeat("x", 32), "second-line", strings.Repeat("y", 60)}
	for _, line := range appends {
		if err := s.AppendText(rel, line); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 3 {
		t.Fatalf("entries = %#v", got)
	}
	if got[0].rel != rel || got[0].epoch != 0 || got[0].off != 0 || got[0].data != appends[0]+"\n" {
		t.Fatalf("first = %#v", got[0])
	}
	if got[1].epoch != 0 || got[1].off != int64(len(got[0].data)) {
		t.Fatalf("second = %#v", got[1])
	}
	// 第三次追加使 33+12+61 > 64，先轮转再写：代际 +1、偏移归零。
	if got[2].epoch != 1 || got[2].off != 0 {
		t.Fatalf("third should start fresh generation = %#v", got[2])
	}
}

func TestReadLogTailSnapsToRuneBoundaryAndReportsPosition(t *testing.T) {
	s := NewGalgameStore(newIO(t.TempDir()))
	rel := "plays/p1/stream.log"
	text := "这是一个较长的中文流式文本"
	if err := s.AppendRaw(rel, text); err != nil {
		t.Fatal(err)
	}
	total := int64(len(text))
	tail, epoch, end, err := s.ReadLogTail(rel, 10)
	if err != nil {
		t.Fatal(err)
	}
	if epoch != 0 || end != total {
		t.Fatalf("position = epoch %d end %d, want 0 %d", epoch, end, total)
	}
	if !utf8.ValidString(tail) {
		t.Fatalf("tail must start on rune boundary: %q", tail)
	}
	if !strings.HasSuffix(text, tail) || tail == "" {
		t.Fatalf("tail %q not a non-empty suffix of %q", tail, text)
	}
	// 不存在的文件：空文本 + 零位置，不算错误。
	missing, epoch2, end2, err := s.ReadLogTail("plays/p1/missing.log", 10)
	if err != nil || missing != "" || epoch2 != 0 || end2 != 0 {
		t.Fatalf("missing = %q %d %d %v", missing, epoch2, end2, err)
	}
}
