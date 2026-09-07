package host

import (
	"context"
	"testing"
	"time"

	"github.com/Leixx98/ai-write-stage/internal/host/imp"
)

func TestImportSessionTransitionsAndBoundedHistory(t *testing.T) {
	manager := newImportSessionManager()
	cancel := func() {}
	if err := manager.begin(imp.Options{SourcePath: `D:\books\novel.txt`, Guidance: "keep prologue", ContinueAfter: true}, cancel); err != nil {
		t.Fatal(err)
	}
	manager.appendEvent(imp.Event{Time: time.Now(), Stage: imp.StageAwaitingConfirmation, Current: 2, Total: 2, Message: "preview", RequiresAction: true})
	status := manager.snapshot()
	if status.State != ImportSessionAwaitingConfirmation || status.Preview != "preview" || !status.CanResume {
		t.Fatalf("unexpected confirmation status: %#v", status)
	}
	if err := manager.begin(imp.Options{ResetGuidance: true}, cancel); err != nil {
		t.Fatal(err)
	}
	if status = manager.snapshot(); status.Guidance != "" {
		t.Fatalf("reset guidance remained in session: %#v", status)
	}
	manager.appendEvent(imp.Event{Time: time.Now(), Stage: imp.StageAwaitingConfirmation, Current: 2, Total: 2, Message: "preview", RequiresAction: true})
	if err := manager.begin(imp.Options{AcceptSegmentation: true}, cancel); err != nil {
		t.Fatal(err)
	}
	manager.appendEvent(imp.Event{Time: time.Now(), Stage: imp.StageAwaitingConfirmation, Current: 2, Total: 2, Message: "confirmed"})
	if status = manager.snapshot(); status.State != ImportSessionRunning || status.CanResume {
		t.Fatalf("accepted confirmation stopped the session: %#v", status)
	}
	for index := 0; index < importHistoryLimit+25; index++ {
		manager.appendEvent(imp.Event{Time: time.Now(), Stage: imp.StageAnalyzing, Current: index, Total: importHistoryLimit + 25, Message: "analyzing"})
	}
	manager.appendEvent(imp.Event{Time: time.Now(), Stage: imp.StageAwaitingStoryStatus, Message: "choose", RequiresAction: true})
	status = manager.snapshot()
	if status.State != ImportSessionAwaitingStoryStatus || len(status.History) != importHistoryLimit {
		t.Fatalf("unexpected story status or history size: state=%s history=%d", status.State, len(status.History))
	}
	manager.appendEvent(imp.Event{Time: time.Now(), Stage: imp.StageDone, Message: "done"})
	if status = manager.snapshot(); status.State != ImportSessionCompleted || status.CanResume {
		t.Fatalf("unexpected completed status: %#v", status)
	}
}

func TestImportSessionCancelIsSafeAndTerminal(t *testing.T) {
	manager := newImportSessionManager()
	cancelled := make(chan struct{})
	if err := manager.begin(imp.Options{}, func() { close(cancelled) }); err != nil {
		t.Fatal(err)
	}
	if !manager.cancelRun() {
		t.Fatal("running import was not cancelled")
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("cancel function was not called")
	}
	manager.appendEvent(imp.Event{Time: time.Now(), Stage: imp.StageError, Message: "cancelled", Err: context.Canceled})
	status := manager.snapshot()
	if status.State != ImportSessionCancelled || !status.CanResume {
		t.Fatalf("unexpected cancelled status: %#v", status)
	}
	if manager.cancelRun() {
		t.Fatal("terminal import was cancelled twice")
	}
}

func TestSuperviseImportKeepsAcceptedConfirmationRunning(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	cancelled := make(chan struct{})
	h := &Host{runCtx: ctx, exclusive: "导入", exclusiveCancel: func() { close(cancelled) }}
	source := make(chan imp.Event, 2)
	output := h.superviseImport(source, imp.Options{})
	source <- imp.Event{Stage: imp.StageAwaitingConfirmation, Message: "confirmed"}
	if event := <-output; event.Message != "confirmed" {
		t.Fatalf("unexpected event: %#v", event)
	}
	select {
	case <-cancelled:
		t.Fatal("accepted confirmation cancelled the running import")
	default:
	}
	source <- imp.Event{Stage: imp.StageAnalyzing, Message: "analyzing"}
	close(source)
	if event := <-output; event.Stage != imp.StageAnalyzing {
		t.Fatalf("import did not continue after confirmation: %#v", event)
	}
	for range output {
	}
	h.asyncWG.Wait()
}

func TestImportSessionRestoreCompletedIsNotResumable(t *testing.T) {
	manager := newImportSessionManager()
	status := manager.restore(ImportSessionCompleted, "Import completed", "")
	if status.State != ImportSessionCompleted || status.CanResume {
		t.Fatalf("unexpected restored completion: %#v", status)
	}
}
