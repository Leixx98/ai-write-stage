package web

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Leixx98/ai-write-stage/internal/bootstrap"
	buildversion "github.com/Leixx98/ai-write-stage/internal/version"
	"github.com/Leixx98/ai-write-stage/internal/workspace"
)

//go:embed static/*
var staticFiles embed.FS

type Options struct {
	Listen    string
	Version   string
	Root      string
	Workspace string
}

// Run starts the browser workbench. The HTTP listener is opened before a book Host is attached.
func Run(cfg bootstrap.Config, build buildversion.Info, opts Options) error {
	listener, listen, err := openListener(opts.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		root, err = workspace.ResolveRoot()
		if err != nil {
			return err
		}
	}
	cfg.FillDefaults()
	wb := newWorkbench(cfg, root)
	defer wb.close()
	initial := strings.TrimSpace(opts.Workspace)
	if initial == "" {
		initial = workspace.LoadLast(root)
	}
	if initial != "" {
		if err := wb.open(initial); err != nil {
			slog.Warn("未能打开工作区", "workspace", initial, "err", err)
		}
	}
	_ = build
	server := &http.Server{
		Addr:              listen,
		Handler:           newHandler(wb),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	fmt.Fprintf(os.Stdout, "ainovel web workbench: http://%s\n", listen)
	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func newHandler(wb *workbench) http.Handler {
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
	mux.HandleFunc("/api/v2/events", wb.events.handler)
	mux.HandleFunc("/api/v2/stream", wb.streams.handler)
	registerV2(mux, wb.ctrl)
	return withNoCache(mux)
}

func withNoCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func Shutdown(ctx context.Context, server *http.Server) error {
	return server.Shutdown(ctx)
}
