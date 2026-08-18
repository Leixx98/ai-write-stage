package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/voocel/ainovel-cli/internal/store"
)

func (c *v2Controller) galgamePlays(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := c.rt.ListPlays()
		if err != nil {
			envelopeErr(w, 500, codeConflict, err)
			return
		}
		envelope(w, 200, 0, items, "")
	case http.MethodPost:
		var req store.PlayMeta
		if err := decodeBody(r, &req); err != nil {
			envelopeErr(w, 400, codeInvalidRequest, err)
			return
		}
		item, err := c.rt.CreatePlay(req)
		if err != nil {
			envelopeErr(w, 422, codeInvalidRequest, err)
			return
		}
		envelope(w, 201, 0, item, "")
	default:
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
	}
}

func (c *v2Controller) galgamePlay(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	if id == "" {
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("play not found"))
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
			return
		}
		view, err := c.rt.PlayView(id)
		if err != nil {
			envelopeErr(w, 404, codeNotFound, err)
			return
		}
		envelope(w, 200, 0, view, "")
		return
	}
	switch parts[1] {
	case "start":
		if r.Method != http.MethodPost {
			envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
			return
		}
		if err := c.rt.StartPlay(id); err != nil {
			envelopeErr(w, 409, codeConflict, err)
			return
		}
		envelope(w, 202, 0, map[string]any{"accepted": true}, "")
	case "pause":
		if r.Method != http.MethodPost {
			envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
			return
		}
		if err := c.rt.PausePlay(); err != nil {
			envelopeErr(w, 409, codeConflict, err)
			return
		}
		envelope(w, 200, 0, map[string]any{"paused": true}, "")
	case "advance":
		if r.Method != http.MethodPost {
			envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
			return
		}
		progress, err := c.rt.AdvancePlay(id)
		if err != nil {
			envelopeErr(w, 409, codeConflict, err)
			return
		}
		view, viewErr := c.rt.PlayView(id)
		if viewErr != nil {
			envelope(w, 200, 0, map[string]any{"progress": progress}, "")
			return
		}
		envelope(w, 200, 0, view, "")
	case "choose":
		if r.Method != http.MethodPost {
			envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
			return
		}
		var req struct {
			ChoiceID string `json:"choice_id"`
		}
		if err := decodeBody(r, &req); err != nil {
			envelopeErr(w, 400, codeInvalidRequest, err)
			return
		}
		if _, err := c.rt.ChoosePlay(id, req.ChoiceID); err != nil {
			envelopeErr(w, 409, codeConflict, err)
			return
		}
		view, err := c.rt.PlayView(id)
		if err != nil {
			envelopeErr(w, 500, codeConflict, err)
			return
		}
		envelope(w, 200, 0, view, "")
	case "beats":
		if r.Method != http.MethodGet {
			envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
			return
		}
		from, _ := strconv.Atoi(r.URL.Query().Get("from"))
		beats, err := c.rt.ListPlayBeats(id, from)
		if err != nil {
			envelopeErr(w, 500, codeConflict, err)
			return
		}
		envelope(w, 200, 0, beats, "")
	default:
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("route not found"))
	}
}
