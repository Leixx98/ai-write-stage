package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/voocel/ainovel-cli/internal/host"
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
		switch r.Method {
		case http.MethodGet:
			view, err := c.rt.PlayView(id)
			if err != nil {
				envelopeErr(w, 404, codeNotFound, err)
				return
			}
			envelope(w, 200, 0, view, "")
		case http.MethodPut:
			var update store.PlayMeta
			if err := decodeBody(r, &update); err != nil {
				envelopeErr(w, 400, codeInvalidRequest, err)
				return
			}
			item, err := c.rt.UpdatePlay(id, update)
			if err != nil {
				envelopeErr(w, 422, codeInvalidRequest, err)
				return
			}
			envelope(w, 200, 0, item, "")
		default:
			envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
		}
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
	case "log":
		if r.Method != http.MethodGet {
			envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
			return
		}
		log, err := c.rt.PlayLog(id)
		if err != nil {
			envelopeErr(w, 404, codeNotFound, err)
			return
		}
		envelope(w, 200, 0, log, "")
	case "log/stream":
		if r.Method != http.MethodGet {
			envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
			return
		}
		c.playLogStream(w, r, id)
	default:
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("route not found"))
	}
}

// playLogStream 是剧场运行日志的 SSE 通道：先订阅（缓冲增量）再发尾部快照，
// 之后按 epoch/off 过滤推增量——快照前的增量 off < 快照末尾会被丢弃，不漏不重。
// 与小说工作台 /api/v2/stream 同构，前端做追加式渲染而非整块轮询替换。
func (c *v2Controller) playLogStream(w http.ResponseWriter, r *http.Request, id string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		envelopeErr(w, 500, codeConflict, fmt.Errorf("streaming unsupported"))
		return
	}
	items, release := c.rt.SubscribePlayLog(id)
	defer release()
	snap, err := c.rt.PlayLogSnapshot(id)
	if err != nil {
		envelopeErr(w, 404, codeNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if !writeSSE(w, map[string]any{"type": "snapshot", "events": snap.Events, "stream": snap.Stream}) {
		return
	}
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case item, open := <-items:
			if !open || !playLogItemAfterSnapshot(item, snap) {
				continue
			}
			if !writeSSE(w, map[string]any{"type": item.Kind, "text": item.Text}) {
				return
			}
			flusher.Flush()
		}
	}
}

// playLogItemAfterSnapshot 判断增量是否严格晚于快照：新代际放行，
// 同代际要求追加起点在快照末尾之后（快照内容已覆盖的部分丢弃）。
func playLogItemAfterSnapshot(item host.PlayLogItem, snap host.PlayLogSnapshot) bool {
	var epoch int
	var end int64
	switch item.Kind {
	case "events":
		epoch, end = snap.EventsEpoch, snap.EventsEnd
	case "stream":
		epoch, end = snap.StreamEpoch, snap.StreamEnd
	default:
		return false
	}
	if item.Epoch != epoch {
		return item.Epoch > epoch
	}
	return item.Off >= end
}
