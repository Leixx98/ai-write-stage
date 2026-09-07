package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Leixx98/ai-write-stage/internal/host"
	"github.com/Leixx98/ai-write-stage/internal/workspace"
)

func (c *v2Controller) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	items, err := workspace.Scan(c.root)
	if err != nil {
		envelopeErr(w, http.StatusInternalServerError, codeConfigInvalid, err)
		return
	}
	c.mu.Lock()
	current := c.name
	c.mu.Unlock()
	envelope(w, http.StatusOK, 0, map[string]any{"items": items, "current": current}, "")
}

func (c *v2Controller) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeBody(r, &req); err != nil {
		envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, err)
		return
	}
	info, err := workspace.Create(c.root, req.Name)
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	envelope(w, http.StatusOK, 0, info, "")
}

func (c *v2Controller) openWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeBody(r, &req); err != nil {
		envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, err)
		return
	}
	if c.wb == nil {
		envelopeErr(w, http.StatusConflict, codeConflict, fmt.Errorf("工作区会话不可用"))
		return
	}
	if err := c.wb.open(strings.TrimSpace(req.Name)); err != nil {
		writeWorkspaceError(w, err)
		return
	}
	envelope(w, http.StatusOK, 0, c.statePayload(), "")
}

func (c *v2Controller) workspaceState(w http.ResponseWriter, r *http.Request) {
	envelope(w, http.StatusOK, 0, c.statePayload(), "")
}

func (c *v2Controller) replayEvents(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	rt := c.rt
	c.mu.Unlock()
	if rt == nil {
		envelope(w, http.StatusOK, 0, []any{}, "")
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	items, err := rt.ReplayQueue(after)
	if err != nil {
		envelopeErr(w, http.StatusInternalServerError, codeConflict, err)
		return
	}
	envelope(w, http.StatusOK, 0, items, "")
}

func (c *v2Controller) statePayload() map[string]any {
	c.mu.Lock()
	rt := c.rt
	name := c.name
	c.mu.Unlock()
	data := map[string]any{
		"workspace":    name,
		"workspace_id": "",
		"snapshot":     host.RuntimeSnapshot{},
	}
	if rt != nil {
		data["snapshot"] = rt.Snapshot()
		data["workspace_id"] = webWorkspaceID(rt.Dir())
	}
	return data
}

func writeWorkspaceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workspace.ErrInvalidName):
		envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, err)
	case errors.Is(err, workspace.ErrNotFound):
		envelopeErr(w, http.StatusNotFound, codeNotFound, err)
	case errors.Is(err, workspace.ErrExists), errors.Is(err, errWorkspaceBusy):
		envelopeErr(w, http.StatusConflict, codeConflict, err)
	default:
		envelopeErr(w, http.StatusConflict, codeConflict, err)
	}
}
