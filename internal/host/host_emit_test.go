package host

import (
	"sync"
	"testing"
)

// 关闭后的 emit 应被明确拒绝，不能依赖 recover 吞掉竞态。
func TestEmitAfterCloseDoesNotPanic(t *testing.T) {
	h := &Host{
		events:   make(chan Event, 1),
		streamCh: make(chan StreamEvent, 1),
		done:     make(chan struct{}, 1),
	}
	h.closeOutputChannels()

	h.emitEvent(Event{Summary: "after close"})
	h.emitStream(StreamEvent{Kind: StreamEventText, Text: "after close"})
}

func TestConcurrentEmitAndCloseDoesNotRaceChannelLifecycle(t *testing.T) {
	h := &Host{
		events:   make(chan Event, 1),
		streamCh: make(chan StreamEvent, 1),
		done:     make(chan struct{}, 1),
	}

	var emitters sync.WaitGroup
	for range 8 {
		emitters.Add(1)
		go func() {
			defer emitters.Done()
			for range 100 {
				h.emitEvent(Event{})
				h.emitStream(StreamEvent{Kind: StreamEventText, Text: "delta"})
			}
		}()
	}
	h.closeOutputChannels()
	emitters.Wait()

	if !h.outputClosed {
		t.Fatal("closeOutputChannels 应原子标记输出已关闭")
	}
}

func TestClosedIsIndependentFromEngineDone(t *testing.T) {
	h := &Host{
		events:   make(chan Event, 1),
		streamCh: make(chan StreamEvent, 1),
		done:     make(chan struct{}, 1),
		closed:   make(chan struct{}),
	}

	h.done <- struct{}{}
	select {
	case <-h.Closed():
		t.Fatal("Engine Done signal must not close the Host lifecycle channel")
	default:
	}

	h.closeOutputChannels()
	select {
	case <-h.Closed():
	default:
		t.Fatal("Host lifecycle channel was not closed during shutdown")
	}
}
