package web

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/Leixx98/ai-write-stage/internal/bootstrap"
)

type setupSaver func(bootstrap.SetupRequest) (bootstrap.Config, error)

func RunSetup(opts Options) (bootstrap.Config, error) {
	listener, listen, err := openListener(opts.Listen)
	if err != nil {
		return bootstrap.Config{}, err
	}
	server := &http.Server{Addr: listen, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute}
	result := make(chan bootstrap.Config, 1)
	var once sync.Once
	server.Handler = newSetupHandler(bootstrap.SaveSetup, func(cfg bootstrap.Config) {
		once.Do(func() {
			result <- cfg
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := server.Shutdown(ctx); err != nil {
					_ = server.Close()
				}
			}()
		})
	})
	fmt.Printf("ainovel first-time setup: http://%s\n", listen)
	err = server.Serve(listener)
	if err != nil && err != http.ErrServerClosed {
		return bootstrap.Config{}, err
	}
	return <-result, nil
}

func newSetupHandler(save setupSaver, complete func(bootstrap.Config)) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		serveSetupAsset(w, "static/setup.html")
	})
	mux.HandleFunc("/static/setup.css", func(w http.ResponseWriter, r *http.Request) {
		serveSetupAsset(w, "static/setup.css")
	})
	mux.HandleFunc("/static/setup.js", func(w http.ResponseWriter, r *http.Request) {
		serveSetupAsset(w, "static/setup.js")
	})
	mux.HandleFunc("/api/setup/providers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
			return
		}
		envelope(w, http.StatusOK, 0, bootstrap.ProviderPresets(), "")
	})
	mux.HandleFunc("/api/setup/test", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
			return
		}
		var request bootstrap.SetupRequest
		if err := decodeBody(r, &request); err != nil {
			envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := bootstrap.TestSetupConnection(ctx, request); err != nil {
			envelopeErr(w, http.StatusUnprocessableEntity, codeUnreachable, err)
			return
		}
		envelope(w, http.StatusOK, 0, map[string]any{"connected": true}, "")
	})
	mux.HandleFunc("/api/setup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
			return
		}
		var request bootstrap.SetupRequest
		if err := decodeBody(r, &request); err != nil {
			envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, err)
			return
		}
		cfg, err := save(request)
		if err != nil {
			envelopeErr(w, http.StatusUnprocessableEntity, codeConfigInvalid, err)
			return
		}
		envelope(w, http.StatusCreated, 0, map[string]any{"saved": true}, "")
		if complete != nil {
			complete(cfg)
		}
	})
	return withNoCache(mux)
}

func serveSetupAsset(w http.ResponseWriter, name string) {
	data, err := staticFiles.ReadFile(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(name))
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	_, _ = w.Write(data)
}
