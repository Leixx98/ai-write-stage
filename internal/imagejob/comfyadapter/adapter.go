package comfyadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/comfyui"
	"github.com/voocel/ainovel-cli/internal/imagejob"
	"github.com/voocel/ainovel-cli/internal/imagejob/service"
	"github.com/voocel/ainovel-cli/internal/store"
)

type Adapter struct {
	store    *store.ComfyUIStore
	config   *store.ImageConfigStore
	prompter imagejob.Prompter
}

const ProviderID = "comfyui"

func New(providerStore *store.ComfyUIStore, config *store.ImageConfigStore, prompter imagejob.Prompter) *Adapter {
	return &Adapter{store: providerStore, config: config, prompter: prompter}
}

func (a *Adapter) Info() imagejob.ProviderInfo {
	info := imagejob.ProviderInfo{
		ID: ProviderID, Name: "ComfyUI", Enabled: a != nil && a.store != nil,
		Capabilities: imagejob.Capabilities{Cancel: true, Progress: true},
	}
	if a == nil || a.store == nil {
		return info
	}
	workflows, _ := a.store.ListWorkflows()
	for _, workflow := range workflows {
		canvas, err := a.store.LoadOrCreateWorkflowCanvas(workflow.ID)
		if err != nil {
			continue
		}
		fields := canvasFieldSet(canvas)
		info.Capabilities.AspectRatio = info.Capabilities.AspectRatio || (fields["width"] && fields["height"])
		info.Capabilities.ImageCount = info.Capabilities.ImageCount || fields["image_count"] || fields["batch_size"]
	}
	return info
}

func (a *Adapter) ValidateProfile(profile imagejob.Profile) error {
	if a == nil || a.store == nil {
		return errors.New("ComfyUI provider is unavailable")
	}
	profile = imagejob.NormalizeProfile(profile)
	workflowID := imagejob.ProviderOptionString(profile, "workflow_id")
	if workflowID == "" {
		return errors.New("ComfyUI profile requires workflow_id")
	}
	workflow, err := a.store.LoadWorkflow(workflowID)
	if err != nil {
		return fmt.Errorf("ComfyUI workflow %q not found", workflowID)
	}
	canvas, err := a.store.LoadOrCreateWorkflowCanvas(workflow.ID)
	if err != nil {
		return err
	}
	fields := canvasFieldSet(canvas)
	capabilities := imagejob.Capabilities{Cancel: true, Progress: true, AspectRatio: fields["width"] && fields["height"], ImageCount: fields["image_count"] || fields["batch_size"]}
	return imagejob.ValidateProfile(profile, capabilities)
}

func (a *Adapter) Execute(ctx context.Context, request imagejob.ProviderRequest, reporter service.Reporter) ([]service.GeneratedOutput, error) {
	if a.prompter == nil {
		return nil, errors.New("image prompt model is unavailable")
	}
	profile := imagejob.NormalizeProfile(request.Profile)
	workflowID := imagejob.ProviderOptionString(profile, "workflow_id")
	workflow, err := a.store.LoadWorkflow(workflowID)
	if err != nil {
		return nil, fmt.Errorf("load ComfyUI workflow: %w", err)
	}
	canvas, err := a.store.LoadOrCreateWorkflowCanvas(workflow.ID)
	if err != nil {
		return nil, fmt.Errorf("load ComfyUI canvas: %w", err)
	}
	schema, err := BuildPromptSchema(workflow.ID, canvas)
	if err != nil {
		return nil, err
	}
	if len(schema.Fields) == 0 {
		return nil, errors.New("ComfyUI workflow exposes no prompt fields")
	}
	if profile.PrompterPresetID != "" && a.config != nil {
		if presets, loadErr := a.config.LoadPrompterPresets(); loadErr == nil {
			if preset, ok := presets.Presets[profile.PrompterPresetID]; ok {
				schema.Composed = imagejob.ComposeSystemPrompt(preset.Template, schema.Fields)
			}
		}
	}
	schemaJSON, err := json.Marshal(schema.Schema)
	if err != nil {
		return nil, err
	}
	promptRequest := scenePromptRequest(request.SceneRequest, schemaJSON, schema)
	promptCtx, cancel := context.WithTimeout(ctx, time.Duration(profile.TimeoutMS)*time.Millisecond)
	raw, err := a.prompter.Generate(promptCtx, promptRequest)
	timedOut := errors.Is(promptCtx.Err(), context.DeadlineExceeded)
	cancel()
	if err != nil {
		if timedOut {
			return nil, errors.New("image prompt generation timed out")
		}
		return nil, err
	}
	reporter.Stage("validating", "validating")
	providerConfig, err := a.store.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("load ComfyUI config: %w", err)
	}
	parsed, err := imagejob.ParseAndValidate(raw, schema, providerConfig.Strict)
	if err != nil {
		return nil, err
	}
	reporter.Prompt(raw, parsed.Values)
	reporter.Stage("binding", "binding")
	values := make(map[string]any, len(parsed.Values)+3)
	for key, value := range parsed.Values {
		values[key] = value
	}
	applyProfileFields(values, canvasFieldSet(canvas), profile)
	if err := MergeCanvasRuntime(&workflow, canvas, values); err != nil {
		return nil, err
	}
	bound, err := comfyui.ApplyBindings(workflow, values)
	if err != nil {
		return nil, err
	}
	instanceID := imagejob.ProviderOptionString(profile, "instance_id")
	if instanceID == "" {
		instanceID = workflow.InstanceID
	}
	if instanceID != "" {
		instances, _, loadErr := a.store.LoadInstances()
		if loadErr != nil {
			return nil, loadErr
		}
		found := false
		for _, instance := range instances {
			if instance.ID == instanceID && instance.Enabled {
				providerConfig.BaseURL = instance.BaseURL
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("ComfyUI instance %q is unavailable", instanceID)
		}
	}
	reporter.ProviderSnapshot(map[string]any{"workflow_id": workflow.ID, "instance_id": instanceID, "workflow": bound, "prompt_values": values})
	client, err := comfyui.NewHTTPClient(providerConfig)
	if err != nil {
		return nil, err
	}
	runCtx, stop := context.WithTimeout(ctx, time.Duration(profile.TimeoutMS)*time.Millisecond)
	defer stop()
	reporter.Stage("submitting", "submitting")
	if err := waitReady(runCtx, client); err != nil {
		return nil, err
	}
	clientID := imageJobClientID(providerConfig.ClientID, request.JobID)
	progressEvents, streamErr := client.OpenProgress(runCtx, clientID)
	if streamErr != nil {
		slog.Debug("comfyui: progress stream unavailable; using history polling", "job_id", request.JobID, "err", streamErr)
		progressEvents = nil
	}
	promptID, err := submitWithDialRetry(runCtx, client, bound, clientID)
	if err != nil {
		return nil, err
	}
	reporter.ExternalID(promptID)
	reporter.Stage("queued", "queued")
	history, err := await(runCtx, client, promptID, progressEvents, providerConfig.PollInterval(), reporter)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			interruptCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = client.Interrupt(interruptCtx)
			cancel()
		}
		return nil, err
	}
	refs := outputRefs(history.Outputs, workflow.Output)
	if len(refs) == 0 {
		return nil, errors.New("ComfyUI returned no image output")
	}
	classified := imagejob.ClassifyOutputs(history.Outputs, request.JobID)
	generated := make([]service.GeneratedOutput, 0, len(refs))
	for index, ref := range refs {
		downloaded, downloadErr := client.Download(runCtx, ref, providerConfig.MaxResponseBytes)
		if downloadErr != nil {
			return nil, downloadErr
		}
		media := imagejob.MediaOutput{Kind: "image", MIME: downloaded.ContentType, Previewable: true}
		if index < len(classified) {
			media = classified[index]
			media.MIME = downloaded.ContentType
		}
		generated = append(generated, service.GeneratedOutput{Media: media, Data: downloaded.Data})
	}
	return generated, nil
}

func (a *Adapter) Cancel(ctx context.Context, externalID string) error {
	config, err := a.store.LoadConfig()
	if err != nil {
		return err
	}
	client, err := comfyui.NewHTTPClient(config)
	if err != nil {
		return err
	}
	return client.Interrupt(ctx)
}

func (a *Adapter) TestConnection(ctx context.Context) error {
	config, err := a.store.LoadConfig()
	if err != nil {
		return err
	}
	client, err := comfyui.NewHTTPClient(config)
	if err != nil {
		return err
	}
	return client.TestConnection(ctx)
}

func scenePromptRequest(request imagejob.SceneImageRequest, schemaJSON []byte, schema imagejob.PromptSchema) imagejob.PromptRequest {
	unitPlan := strings.TrimSpace(request.VisualIntent)
	if len(request.Characters) > 0 {
		data, _ := json.Marshal(request.Characters)
		unitPlan = strings.TrimSpace(unitPlan + "\ncharacters=" + string(data))
	}
	text := strings.TrimSpace(strings.Join([]string{request.Text, request.Dialogue}, "\n"))
	return imagejob.PromptRequest{
		UnitID: request.UnitID, Chapter: request.Chapter, Ordinal: request.Ordinal,
		ChapterTitle: request.Title, UnitPlan: unitPlan, UnitText: text,
		PreviousTail: request.PreviousText, Schema: schemaJSON, SchemaHash: schema.SchemaHash,
		SystemPrompt: schema.Composed,
	}
}

func canvasFieldSet(canvas comfyui.CanvasDocument) map[string]bool {
	fields := map[string]bool{}
	for _, field := range canvas.Fields {
		fields[strings.ToLower(strings.TrimSpace(field.ID))] = true
	}
	return fields
}

func applyProfileFields(values map[string]any, fields map[string]bool, profile imagejob.Profile) {
	if fields["image_count"] {
		values["image_count"] = profile.ImageCount
	}
	if fields["batch_size"] {
		values["batch_size"] = profile.ImageCount
	}
	if fields["width"] && fields["height"] && profile.AspectRatio != "" {
		parts := strings.Split(profile.AspectRatio, ":")
		if len(parts) == 2 {
			widthRatio, widthErr := strconv.Atoi(parts[0])
			heightRatio, heightErr := strconv.Atoi(parts[1])
			if widthErr == nil && heightErr == nil && widthRatio > 0 && heightRatio > 0 {
				const base = 1024
				if widthRatio >= heightRatio {
					values["width"] = base
					values["height"] = round64(base * heightRatio / widthRatio)
				} else {
					values["height"] = base
					values["width"] = round64(base * widthRatio / heightRatio)
				}
			}
		}
	}
}

func round64(value int) int {
	if value < 64 {
		return 64
	}
	return ((value + 32) / 64) * 64
}

func imageJobClientID(configured, jobID string) string {
	base := strings.TrimSpace(configured)
	if base == "" {
		base = "ainovel"
	}
	return base + "-" + jobID
}

func waitReady(ctx context.Context, client *comfyui.HTTPClient) error {
	delays := []time.Duration{0, 200 * time.Millisecond, 500 * time.Millisecond, time.Second}
	var lastErr error
	for attempt, delay := range delays {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := client.TestConnection(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		slog.Debug("comfyui: readiness check failed", "attempt", attempt+1, "err", err)
	}
	return fmt.Errorf("ComfyUI is not ready: %w", lastErr)
}

func submitWithDialRetry(ctx context.Context, client *comfyui.HTTPClient, workflow map[string]any, clientID string) (string, error) {
	delays := []time.Duration{0, 200 * time.Millisecond, 500 * time.Millisecond}
	var lastErr error
	for attempt, delay := range delays {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", ctx.Err()
			case <-timer.C:
			}
		}
		promptID, err := client.Submit(ctx, workflow, clientID)
		if err == nil {
			return promptID, nil
		}
		lastErr = err
		var opErr *net.OpError
		if !errors.As(err, &opErr) || opErr.Op != "dial" || attempt == len(delays)-1 {
			break
		}
	}
	return "", lastErr
}

func await(ctx context.Context, client *comfyui.HTTPClient, promptID string, events <-chan comfyui.ProgressEvent, interval time.Duration, reporter service.Reporter) (comfyui.History, error) {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	check := func() (comfyui.History, bool, error) {
		history, done, err := client.CheckHistory(ctx, promptID)
		if err != nil || !done {
			return history, done, err
		}
		return history, true, history.Error
	}
	if history, done, err := check(); err != nil || done {
		return history, err
	}
	for {
		select {
		case <-ctx.Done():
			return comfyui.History{PromptID: promptID}, ctx.Err()
		case event, open := <-events:
			if !open {
				events = nil
				continue
			}
			if event.PromptID != promptID {
				continue
			}
			if event.Status == "execution_start" || event.Status == "executing" || event.Status == "progress" {
				reporter.Stage("running", "running")
			}
			if event.Status == "progress" && event.Total > 0 {
				reporter.Progress(event.Current, event.Total, event.Node)
			}
			if terminalEvent(event.Status) {
				if history, done, err := check(); err != nil || done {
					return history, err
				}
			}
		case <-ticker.C:
			if history, done, err := check(); err != nil || done {
				return history, err
			}
		}
	}
}

func terminalEvent(status string) bool {
	return status == "execution_success" || status == "execution_error" || status == "execution_interrupted"
}

func outputRefs(outputs map[string]any, preferred comfyui.OutputSpec) []comfyui.OutputRef {
	var refs []comfyui.OutputRef
	appendEntries := func(raw any) {
		items, ok := raw.([]any)
		if !ok {
			return
		}
		for _, item := range items {
			value, ok := item.(map[string]any)
			if !ok || strings.TrimSpace(fmt.Sprint(value["filename"])) == "" {
				continue
			}
			refs = append(refs, comfyui.OutputRef{Filename: fmt.Sprint(value["filename"]), Subfolder: fmt.Sprint(value["subfolder"]), Type: fmt.Sprint(value["type"])})
		}
	}
	if node, ok := outputs[preferred.NodeID].(map[string]any); ok && preferred.Path != "" {
		appendEntries(node[preferred.Path])
	}
	if len(refs) > 0 {
		return refs
	}
	nodes := make([]string, 0, len(outputs))
	for node := range outputs {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	for _, nodeID := range nodes {
		node, ok := outputs[nodeID].(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"images", "gifs", "files"} {
			appendEntries(node[key])
		}
	}
	return refs
}
