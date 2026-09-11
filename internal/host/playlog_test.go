package host

import (
	"testing"
)

func TestParsePlayLogRel(t *testing.T) {
	cases := []struct {
		rel  string
		id   string
		kind string
		ok   bool
	}{
		{"plays/p1/runtime.log", "p1", "events", true},
		{"plays/p1/stream.log", "p1", "stream", true},
		{"plays/p1/calls.jsonl", "", "", false},
		{"plays/p1/writer_session.json", "", "", false},
		{"sessions/s1/runtime.log", "", "", false},
		{"runtime.log", "", "", false},
		{"plays//runtime.log", "", "", false},
		{"plays/p1", "", "", false},
		{"galgame/plays/p1/runtime.log", "", "", false},
		{"output/novel/meta/decisions.jsonl", "", "", false},
	}
	for _, tc := range cases {
		id, kind, ok := parsePlayLogRel(tc.rel)
		if id != tc.id || kind != tc.kind || ok != tc.ok {
			t.Errorf("parsePlayLogRel(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.rel, id, kind, ok, tc.id, tc.kind, tc.ok)
		}
	}
}

func TestPlayLogBrokerRoutesByPlayAndSkipsForeignRels(t *testing.T) {
	b := newPlayLogBroker()
	idA, chA := b.subscribe("pA")
	_, chB := b.subscribe("pB")
	defer b.unsubscribe(idA)

	b.observe("plays/pA/runtime.log", 0, 0, []byte("line-a\n"))
	b.observe("plays/pB/stream.log", 1, 42, []byte("delta"))
	b.observe("plays/pA/calls.jsonl", 0, 0, []byte(`{}`))
	b.observe("sessions/s1/runtime.log", 0, 0, []byte("chat\n"))

	select {
	case item := <-chA:
		if item.Kind != "events" || item.Text != "line-a\n" || item.Epoch != 0 || item.Off != 0 {
			t.Fatalf("pA item = %#v", item)
		}
	default:
		t.Fatal("pA item not delivered")
	}
	select {
	case item := <-chB:
		if item.Kind != "stream" || item.Epoch != 1 || item.Off != 42 || item.Text != "delta" {
			t.Fatalf("pB item = %#v", item)
		}
	default:
		t.Fatal("pB item not delivered")
	}
	if len(chA) != 0 || len(chB) != 0 {
		t.Fatalf("foreign rels leaked: chA=%d chB=%d", len(chA), len(chB))
	}
}

func TestPlayLogBrokerFullBufferDoesNotBlockWriter(t *testing.T) {
	b := newPlayLogBroker()
	_, ch := b.subscribe("pA")
	// 慢消费者不取数据；写路径必须非阻塞（满则丢）。
	for i := 0; i < playLogClientBuffer+16; i++ {
		b.observe("plays/pA/stream.log", 0, int64(i), []byte("x"))
	}
	if len(ch) != playLogClientBuffer {
		t.Fatalf("buffer = %d, want %d", len(ch), playLogClientBuffer)
	}
}

func TestSubscribePlayLogNilHostIsSafe(t *testing.T) {
	var h *Host
	items, release := h.SubscribePlayLog("p1")
	defer release()
	if _, open := <-items; open {
		t.Fatal("nil host channel must be closed")
	}
}
