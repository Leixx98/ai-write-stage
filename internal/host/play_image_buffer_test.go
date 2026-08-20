package host

import (
	"fmt"
	"testing"

	"github.com/voocel/ainovel-cli/internal/galgame/play"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestSummarizePlayImageBufferHidesHistoricalFailureAfterNewerSuccess(t *testing.T) {
	beats := []store.PlayBeat{
		{Ordinal: 31, CG: store.PlayCGNew, ImageJobID: "failed-old"},
		{Ordinal: 71, CG: store.PlayCGNew, ImageJobID: "completed-new"},
	}
	jobs := map[string]store.ImageJob{
		"failed-old":    {JobID: "failed-old", Status: "failed", Error: "connection refused"},
		"completed-new": {JobID: "completed-new", Status: "completed"},
	}
	buffer := summarizePlayImageBuffer(play.BufferInfo{}, beats, 72, jobLoader(jobs))
	if buffer.ImageGenerating || buffer.ImageError != "" {
		t.Fatalf("settled buffer = %+v", buffer)
	}
}

func TestSummarizePlayImageBufferPrefersActiveJob(t *testing.T) {
	beats := []store.PlayBeat{
		{Ordinal: 31, CG: store.PlayCGNew, ImageJobID: "failed-old"},
		{Ordinal: 70, CG: store.PlayCGNew, ImageJobID: "running-new"},
	}
	jobs := map[string]store.ImageJob{
		"failed-old":  {JobID: "failed-old", Status: "failed", Error: "connection refused"},
		"running-new": {JobID: "running-new", Status: "running", ProgressCurrent: 7, ProgressTotal: 20, ProgressNode: "12"},
	}
	buffer := summarizePlayImageBuffer(play.BufferInfo{}, beats, 70, jobLoader(jobs))
	if !buffer.ImageGenerating || buffer.ImageProgress != 35 || buffer.ImageProgressNode != "12" || buffer.ImageError != "" {
		t.Fatalf("active buffer = %+v", buffer)
	}
}

func TestSummarizePlayImageBufferShowsLatestFailure(t *testing.T) {
	beats := []store.PlayBeat{
		{Ordinal: 31, CG: store.PlayCGNew, ImageJobID: "completed-old"},
		{Ordinal: 36, CG: store.PlayCGNew, ImageJobID: "failed-new"},
	}
	jobs := map[string]store.ImageJob{
		"completed-old": {JobID: "completed-old", Status: "completed"},
		"failed-new":    {JobID: "failed-new", Status: "failed", Error: "CUDA error"},
	}
	buffer := summarizePlayImageBuffer(play.BufferInfo{}, beats, 36, jobLoader(jobs))
	if buffer.ImageGenerating || buffer.ImageError != "CUDA error" {
		t.Fatalf("failed buffer = %+v", buffer)
	}
}

func jobLoader(jobs map[string]store.ImageJob) func(string) (store.ImageJob, error) {
	return func(id string) (store.ImageJob, error) {
		job, ok := jobs[id]
		if !ok {
			return store.ImageJob{}, fmt.Errorf("job not found")
		}
		return job, nil
	}
}
