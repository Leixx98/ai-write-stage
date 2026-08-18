package play

import (
	"strings"

	"github.com/voocel/ainovel-cli/internal/store"
)

const (
	ImagePending   = "pending"
	ImageCompleted = "completed"
	ImageFailed    = "failed"
)

type ImageInfo struct {
	JobID   string `json:"job_id,omitempty"`
	Status  string `json:"status,omitempty"`
	URL     string `json:"url,omitempty"`
	Ordinal int    `json:"ordinal,omitempty"`
}

type BufferInfo struct {
	TextAhead     int `json:"text_ahead"`
	ImagesPending int `json:"images_pending"`
}

type View struct {
	Play     store.PlayMeta     `json:"play"`
	Progress store.PlayProgress `json:"progress"`
	Beat     *store.PlayBeat    `json:"beat,omitempty"`
	Image    ImageInfo          `json:"image"`
	Buffer   BufferInfo         `json:"buffer"`
}

func BuildView(meta store.PlayMeta, progress store.PlayProgress, beats []store.PlayBeat) View {
	view := View{Play: meta, Progress: progress, Buffer: BufferInfo{
		TextAhead: progress.WriteHead - progress.PlayHead,
	}}
	if progress.PlayHead < 1 {
		return view
	}
	for i := range beats {
		if beats[i].Ordinal == progress.PlayHead {
			view.Beat = &beats[i]
			break
		}
	}
	bound := store.DisplayBoundImage(beats, progress.PlayHead)
	view.Image = ImageInfo{JobID: bound.JobID, Ordinal: bound.Ordinal}
	if bound.Error != "" && bound.JobID == "" {
		view.Image.Status = ImageFailed
	} else if view.Image.Ordinal > 0 && view.Image.JobID == "" {
		view.Image.Status = ImagePending
	}
	view.Buffer.ImagesPending = CountUnfinishedImages(beats, progress.WriteHead, nil)
	return view
}

func CountUnfinishedImages(beats []store.PlayBeat, writeHead int, completed func(jobID string) bool) int {
	pending := 0
	for _, beat := range beats {
		if beat.CG != store.PlayCGNew || beat.Ordinal > writeHead || beat.Ordinal < 1 {
			continue
		}
		id := strings.TrimSpace(beat.ImageJobID)
		if id == "" && strings.TrimSpace(beat.ImageError) != "" {
			continue
		}
		if id != "" && completed != nil && completed(id) {
			continue
		}
		pending++
	}
	return pending
}

func (v *View) ApplyImageJob(status, url string, loadErr error) {
	if v == nil || v.Image.Ordinal < 1 {
		return
	}
	v.Image.URL = ""
	if v.Image.JobID == "" {
		if v.Image.Status != ImageFailed {
			v.Image.Status = ImagePending
		}
		if loadErr != nil {
			v.Image.Status = ImageFailed
		}
		return
	}
	status = strings.TrimSpace(status)
	if loadErr != nil {
		v.Image.Status = ImagePending
		return
	}
	if status == ImageCompleted && strings.TrimSpace(url) != "" {
		v.Image.Status = ImageCompleted
		v.Image.URL = url
		return
	}
	if status == "" {
		v.Image.Status = ImagePending
		return
	}
	v.Image.Status = status
}
