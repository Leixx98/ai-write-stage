package host

import (
	"context"
	"testing"
	"time"
)

func TestCompletedUnitWatcherStopsWhenRuntimeIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := &Host{runCtx: ctx}
	h.startCompletedUnitWatcher()
	cancel()
	done := make(chan struct{})
	go func() {
		h.asyncWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("completed unit watcher did not stop")
	}
}
