package play

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/store"
)

func TestBuildViewBindsImageToCurrentNewBeat(t *testing.T) {
	beats := []store.PlayBeat{
		{Ordinal: 1, Kind: store.BeatDialogue, Text: "一", CG: store.PlayCGNew, ImageJobID: "job_a"},
		{Ordinal: 2, Kind: store.BeatDialogue, Text: "二", CG: store.PlayCGKeep},
		{Ordinal: 3, Kind: store.BeatDialogue, Text: "三", CG: store.PlayCGNew},
	}
	view := BuildView(store.PlayMeta{}, store.PlayProgress{PlayHead: 2, WriteHead: 3}, beats)
	if view.Image.JobID != "job_a" || view.Image.Ordinal != 1 {
		t.Fatalf("keep beat image = %+v", view.Image)
	}
	if view.Buffer.ImagesPending != 2 {
		t.Fatalf("pending without job status = %d", view.Buffer.ImagesPending)
	}
	view = BuildView(store.PlayMeta{}, store.PlayProgress{PlayHead: 3, WriteHead: 3}, beats)
	if view.Image.JobID != "" || view.Image.Ordinal != 3 || view.Image.Status != ImagePending {
		t.Fatalf("unstarted new beat must not reuse previous image = %+v", view.Image)
	}
	beats[2].ImageError = "comfy down"
	view = BuildView(store.PlayMeta{}, store.PlayProgress{PlayHead: 3, WriteHead: 3}, beats)
	if view.Image.Status != ImageFailed {
		t.Fatalf("start failure should surface as failed = %+v", view.Image)
	}
	view.ApplyImageJob("", "", nil)
	if view.Image.Status != ImageFailed {
		t.Fatalf("apply without job must keep failed = %+v", view.Image)
	}
}

func TestApplyImageJobOnlyExposesCompletedURL(t *testing.T) {
	view := View{Image: ImageInfo{JobID: "job_b", Ordinal: 3}}
	view.ApplyImageJob("running", "http://x/job_b", nil)
	if view.Image.URL != "" || view.Image.Status != "running" {
		t.Fatalf("running job must not expose url = %+v", view.Image)
	}
	view.ApplyImageJob(ImageCompleted, "/api/v2/comfyui/jobs/job_b/outputs/0", nil)
	if view.Image.Status != ImageCompleted || view.Image.URL == "" {
		t.Fatalf("completed job = %+v", view.Image)
	}
	view.Image.URL = "stale"
	view.ApplyImageJob("", "", nil)
	if view.Image.Status != ImagePending || view.Image.URL != "" {
		t.Fatalf("missing status = %+v", view.Image)
	}
}

func TestCountUnfinishedImagesSkipsCompletedJobs(t *testing.T) {
	beats := []store.PlayBeat{
		{Ordinal: 1, CG: store.PlayCGNew, ImageJobID: "job_a"},
		{Ordinal: 2, CG: store.PlayCGKeep},
		{Ordinal: 3, CG: store.PlayCGNew, ImageJobID: "job_b"},
		{Ordinal: 4, CG: store.PlayCGNew},
	}
	done := map[string]bool{"job_a": true}
	got := CountUnfinishedImages(beats, 4, func(id string) bool { return done[id] })
	if got != 2 {
		t.Fatalf("pending = %d", got)
	}
}
