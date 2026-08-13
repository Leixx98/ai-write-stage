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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/entry/startup"
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

	// Match the TUI bootstrap behavior: an existing unfinished book resumes on startup.
	if _, err := rt.Resume(); err != nil {
		return fmt.Errorf("resume: %w", err)
	}

	server := &http.Server{
		Addr:              listen,
		Handler:           newHandler(rt, build.Version),
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

func newHandler(rt *host.Host, version string) http.Handler {
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
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"version":  version,
			"snapshot": rt.Snapshot(),
			"dir":      rt.Dir(),
		})
	})
	mux.HandleFunc("/api/replay", func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		items, err := rt.ReplayQueue(after)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	})
	mux.HandleFunc("/api/events", events.handler)
	mux.HandleFunc("/api/stream", streams.handler)
	mux.HandleFunc("/api/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		plan, err := startup.PrepareQuick(startup.Request{Mode: startup.ModeQuick, UserPrompt: req.Prompt, OutputDir: rt.Dir(), Interactive: true})
		if err == nil {
			err = rt.PrepareUserRules(plan.RawPrompt)
		}
		if err == nil {
			err = rt.StartPrepared(plan.RawPrompt)
		}
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
	})
	mux.HandleFunc("/api/continue", commandHandler(func(rt *host.Host, text string) error { return rt.Continue(text) }, rt))
	mux.HandleFunc("/api/steer", commandHandler(func(rt *host.Host, text string) error { return rt.Steer(text) }, rt))
	mux.HandleFunc("/api/abort", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"stopped": rt.Abort()})
	})
	mux.HandleFunc("/api/units/", unitMediaHandler(rt))
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

func commandHandler(fn func(*host.Host, string) error, rt *host.Host) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := fn(rt, strings.TrimSpace(req.Text)); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
	}
}

func eventStream(rt *host.Host) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		for {
			select {
			case <-r.Context().Done():
				return
			case ev, ok := <-rt.Events():
				if !ok {
					return
				}
				if !writeSSE(w, ev) {
					return
				}
				flusher.Flush()
			}
		}
	}
}

func textStream(rt *host.Host) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		for {
			select {
			case <-r.Context().Done():
				return
			case delta, ok := <-rt.Stream():
				if !ok {
					return
				}
				payload := map[string]any{"delta": delta}
				if delta == host.StreamClearSentinel {
					payload = map[string]any{"clear": true}
				}
				if !writeSSE(w, payload) {
					return
				}
				flusher.Flush()
			}
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

func unitMediaHandler(rt *host.Host) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 4 || parts[0] != "api" || parts[1] != "units" {
			http.NotFound(w, r)
			return
		}
		chapter, err1 := strconv.Atoi(parts[2])
		ordinal, err2 := strconv.Atoi(parts[3])
		if err1 != nil || err2 != nil || chapter <= 0 || ordinal <= 0 || chapter > 100000 || ordinal > 100000 {
			http.Error(w, "invalid unit", http.StatusBadRequest)
			return
		}
		base := filepath.Join(rt.Dir(), "drafts", fmt.Sprintf("%02d.units", chapter), fmt.Sprintf("%03d", ordinal))
		for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp"} {
			path := base + ext
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				http.ServeFile(w, r, path)
				return
			}
		}
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
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
