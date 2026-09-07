package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/Leixx98/ai-write-stage/internal/host"
)

type eventBroker struct {
	mu      sync.Mutex
	nextID  int
	gen     int
	clients map[int]chan host.Event
}

func newEventBroker() *eventBroker {
	return &eventBroker{clients: make(map[int]chan host.Event)}
}

func (b *eventBroker) follow(rt *host.Host) {
	b.mu.Lock()
	b.gen++
	gen := b.gen
	b.mu.Unlock()
	if rt == nil {
		return
	}
	go func(src <-chan host.Event, gen int) {
		for ev := range src {
			b.mu.Lock()
			if b.gen != gen {
				b.mu.Unlock()
				return
			}
			for _, ch := range b.clients {
				select {
				case ch <- ev:
				default:
				}
			}
			b.mu.Unlock()
		}
	}(rt.Events(), gen)
}

func (b *eventBroker) handler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch := make(chan host.Event, 32)
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.clients[id] = ch
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.clients, id); b.mu.Unlock() }()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok || !writeSSE(w, ev) {
				return
			}
			flusher.Flush()
		}
	}
}

type streamBroker struct {
	mu      sync.Mutex
	nextID  int
	gen     int
	clients map[int]chan map[string]any
}

func newStreamBroker() *streamBroker {
	return &streamBroker{clients: make(map[int]chan map[string]any)}
}

func (b *streamBroker) follow(rt *host.Host) {
	b.mu.Lock()
	b.gen++
	gen := b.gen
	b.mu.Unlock()
	if rt == nil {
		return
	}
	go func(src <-chan host.StreamEvent, gen int) {
		for event := range src {
			payload := map[string]any{"kind": event.Kind}
			switch event.Kind {
			case host.StreamEventClear:
				payload = map[string]any{"clear": true}
			case host.StreamEventTool:
				payload["tool"] = event.Tool
			case host.StreamEventText, host.StreamEventThinking:
				payload["delta"] = event.Text
			}
			b.mu.Lock()
			if b.gen != gen {
				b.mu.Unlock()
				return
			}
			for _, ch := range b.clients {
				select {
				case ch <- payload:
				default:
				}
			}
			b.mu.Unlock()
		}
	}(rt.Stream(), gen)
}

func (b *streamBroker) handler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch := make(chan map[string]any, 64)
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.clients[id] = ch
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.clients, id); b.mu.Unlock() }()
	for {
		select {
		case <-r.Context().Done():
			return
		case payload, ok := <-ch:
			if !ok || !writeSSE(w, payload) {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, value any) bool {
	b, err := json.Marshal(value)
	if err != nil {
		return false
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err == nil
}
