package bootstrap

import "testing"

func TestCompactReserveTokensCapsSmallWindowAtHalf(t *testing.T) {
	if got := CompactReserveTokens(8192); got != 4096 {
		t.Fatalf("8K reserve = %d, want 4096", got)
	}
	if got := CompactReserveTokens(16384); got != 8000 {
		t.Fatalf("16K reserve = %d, want 8000", got)
	}
	if got := CompactReserveTokens(32768); got != 8000 {
		t.Fatalf("32K reserve = %d, want 8000", got)
	}
}
