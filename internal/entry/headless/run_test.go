package headless

import (
	"bytes"
	"testing"

	"github.com/voocel/ainovel-cli/internal/host"
)

func TestWriteStreamEventFormatsAtEntryBoundary(t *testing.T) {
	var output bytes.Buffer
	hasContent, err := writeStreamEvent(&output, host.StreamEvent{Kind: host.StreamEventTool, Tool: "规划"}, false)
	if err != nil {
		t.Fatal(err)
	}
	hasContent, err = writeStreamEvent(&output, host.StreamEvent{Kind: host.StreamEventThinking, Text: "分析"}, hasContent)
	if err != nil {
		t.Fatal(err)
	}
	if !hasContent {
		t.Fatal("expected content")
	}
	if got := output.String(); got != "▸ 规划\n分析" {
		t.Fatalf("got %q", got)
	}
}
