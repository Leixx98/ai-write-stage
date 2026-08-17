package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
	buildversion "github.com/voocel/ainovel-cli/internal/version"
)

//go:embed static/*
var staticFiles embed.FS

type Options struct {
	Listen  string
	Version string
}

// Run starts the browser workbench and keeps the same Host/Engine used by the TUI.
func Run(cfg bootstrap.Config, bundle assets.Bundle, build buildversion.Info, opts Options) error {
	listen := strings.TrimSpace(opts.Listen)
	if listen == "" {
		listen = "127.0.0.1:8080"
	}
	// Keep the web entry's logs separate from an interactive TUI session.
	rt, err := host.New(cfg, bundle, host.WithFileLog("web.log", false))
	if err != nil {
		return err
	}
	defer rt.Close()
	_ = build

	// Web startup only restores the host and exposes the saved progress. Keep
	// the engine paused until the user explicitly clicks Continue in the UI.

	server := &http.Server{
		Addr:              listen,
		Handler:           newHandler(rt),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	fmt.Fprintf(os.Stdout, "ainovel web workbench: http://%s\n", listen)
	err = server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func newHandler(rt *host.Host) http.Handler {
	events := newEventBroker(rt)
	streams := newStreamBroker(rt)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := staticFiles.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		data, err := staticFiles.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(name)))
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/api/v2/events", events.handler)
	mux.HandleFunc("/api/v2/stream", streams.handler)
	registerV2(mux, rt)
	return withNoCache(mux)
}

type eventBroker struct {
	mu      sync.Mutex
	nextID  int
	clients map[int]chan host.Event
}

func newEventBroker(rt *host.Host) *eventBroker {
	b := &eventBroker{clients: make(map[int]chan host.Event)}
	go func() {
		for ev := range rt.Events() {
			b.mu.Lock()
			for _, ch := range b.clients {
				select {
				case ch <- ev:
				default:
				}
			}
			b.mu.Unlock()
		}
		b.mu.Lock()
		for id, ch := range b.clients {
			close(ch)
			delete(b.clients, id)
		}
		b.mu.Unlock()
	}()
	return b
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
	clients map[int]chan map[string]any
}

func newStreamBroker(rt *host.Host) *streamBroker {
	b := &streamBroker{clients: make(map[int]chan map[string]any)}
	go func() {
		for delta := range rt.Stream() {
			payload := map[string]any{"delta": delta}
			if delta == host.StreamClearSentinel {
				payload = map[string]any{"clear": true}
			}
			b.mu.Lock()
			for _, ch := range b.clients {
				select {
				case ch <- payload:
				default:
				}
			}
			b.mu.Unlock()
		}
		b.mu.Lock()
		for id, ch := range b.clients {
			close(ch)
			delete(b.clients, id)
		}
		b.mu.Unlock()
	}()
	return b
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

func withNoCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// Shutdown is provided for callers embedding the workbench in another process.
func Shutdown(ctx context.Context, server *http.Server) error {
	return server.Shutdown(ctx)
}
