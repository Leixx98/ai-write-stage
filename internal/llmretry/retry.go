package llmretry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/voocel/agentcore"
)

const maxRetryDelay = 60 * time.Second
const StreamMaxRetries = 5

// Generator 是请求重试所需的最小模型接口。
type Generator interface {
	Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error)
}

// StreamGenerator 是流式调用所需的最小模型接口。
type StreamGenerator interface {
	GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error)
}

// Event 描述一次即将发生的请求重试。
type Event struct {
	Attempt int
	Delay   time.Duration
	Err     error
}

// Config 配置重试的可观测信息，不改变重试语义。
type Config struct {
	Agent    string
	OnRetry  func(Event)
	OnStream func(agentcore.StreamEvent)
}

// Generate 调用 model.Generate。retryable 错误退避后持续重试，直到成功或
// context 结束；非 retryable 错误立即返回。
func Generate(ctx context.Context, model Generator, cfg Config, messages []agentcore.Message, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	for retry := 1; ; retry++ {
		resp, err := model.Generate(ctx, messages, nil, opts...)
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || !isRetryable(err) {
			return nil, err
		}
		delay := retryDelay(err, retry-1)
		event := Event{Attempt: retry, Delay: delay, Err: err}
		if cfg.OnRetry != nil {
			cfg.OnRetry(event)
		}
		meta, _ := json.Marshal(struct {
			DelayMS int64 `json:"retry_delay_ms"`
		}{DelayMS: delay.Milliseconds()})
		agentcore.ReportToolProgress(ctx, agentcore.ProgressPayload{
			Kind:    agentcore.ProgressRetry,
			Agent:   cfg.Agent,
			Attempt: retry,
			Message: err.Error(),
			Meta:    meta,
		})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// GenerateStream 调用 model.GenerateStream 并攒成一次完整响应。retryable 错误
// 退避后重试，最多 StreamMaxRetries 次；超过上限立即返回，不再跟 context 无限耗。
func GenerateStream(ctx context.Context, model StreamGenerator, cfg Config, messages []agentcore.Message, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if model == nil {
		return nil, fmt.Errorf("模型未配置")
	}
	var last error
	for retry := 0; ; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resp, err := collectStream(ctx, model, cfg, messages, opts)
		if err == nil {
			return resp, nil
		}
		last = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || !isRetryable(err) {
			return nil, err
		}
		if retry >= StreamMaxRetries {
			return nil, fmt.Errorf("流式调用 retryable 已达 %d 次上限: %w", StreamMaxRetries, last)
		}
		retry++
		delay := retryDelay(err, retry-1)
		event := Event{Attempt: retry, Delay: delay, Err: err}
		if cfg.OnRetry != nil {
			cfg.OnRetry(event)
		}
		meta, _ := json.Marshal(struct {
			DelayMS int64 `json:"retry_delay_ms"`
		}{DelayMS: delay.Milliseconds()})
		agentcore.ReportToolProgress(ctx, agentcore.ProgressPayload{
			Kind:    agentcore.ProgressRetry,
			Agent:   cfg.Agent,
			Attempt: retry,
			Message: err.Error(),
			Meta:    meta,
		})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func collectStream(ctx context.Context, model StreamGenerator, cfg Config, messages []agentcore.Message, opts []agentcore.CallOption) (*agentcore.LLMResponse, error) {
	events, err := model.GenerateStream(ctx, messages, nil, opts...)
	if err != nil {
		return nil, err
	}
	var text strings.Builder
	var done *agentcore.LLMResponse
	var streamErr error
	for ev := range events {
		if cfg.OnStream != nil {
			cfg.OnStream(ev)
		}
		switch ev.Type {
		case agentcore.StreamEventTextDelta:
			text.WriteString(ev.Delta)
		case agentcore.StreamEventDone:
			msg := ev.Message
			if msg.TextContent() == "" && text.Len() > 0 {
				msg.Content = []agentcore.ContentBlock{agentcore.TextBlock(text.String())}
			}
			if ev.StopReason != "" {
				msg.StopReason = ev.StopReason
			}
			done = &agentcore.LLMResponse{Message: msg}
		case agentcore.StreamEventError:
			if ev.Err != nil {
				streamErr = ev.Err
			}
		}
	}
	if streamErr != nil {
		return nil, streamErr
	}
	if done == nil {
		return nil, agentcore.ErrStreamPartial
	}
	return done, nil
}

func isRetryable(err error) bool {
	var retryable agentcore.RetryableError
	return errors.As(err, &retryable) && retryable.Retryable()
}

func retryDelay(err error, attempt int) time.Duration {
	var hinter agentcore.RetryHinter
	if errors.As(err, &hinter) {
		if delay := hinter.RetryAfter(); delay > 0 {
			if delay > maxRetryDelay {
				return maxRetryDelay
			}
			return delay
		}
	}
	delay := time.Second
	for i := 0; i < attempt && delay < maxRetryDelay; i++ {
		delay *= 2
	}
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}
