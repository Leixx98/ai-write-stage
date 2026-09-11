package runlog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/voocel/agentcore"
)

const HeartbeatInterval = 15 * time.Second

const (
	ModeChat = "chat"
	ModePlay = "play"

	EventStart      = "start"
	EventHeartbeat  = "heartbeat"
	EventFirstToken = "first_token"
	EventFinish     = "finish"
	EventNote       = "note"
	EventRetry      = "retry"
	EventCorrect    = "correction"
	EventRepair     = "repair"

	IndexRel = "galgame/runtime.log"
)

type Sink interface {
	AppendJSONL(rel string, v any) error
	AppendText(rel string, line string) error
}

type Record struct {
	TS              time.Time `json:"ts"`
	Event           string    `json:"event"`
	Mode            string    `json:"mode"`
	Step            string    `json:"step,omitempty"`
	CallID          string    `json:"call_id,omitempty"`
	SessionID       string    `json:"session_id,omitempty"`
	CharacterID     string    `json:"character_id,omitempty"`
	PlayID          string    `json:"play_id,omitempty"`
	Provider        string    `json:"provider,omitempty"`
	Model           string    `json:"model,omitempty"`
	Thinking        string    `json:"thinking,omitempty"`
	Protocol        string    `json:"protocol,omitempty"`
	MaxTokens       int       `json:"max_tokens,omitempty"`
	PromptChars     int       `json:"prompt_chars,omitempty"`
	OutputChars     int       `json:"output_chars,omitempty"`
	ThinkingChars   int       `json:"thinking_chars,omitempty"`
	ThinkingSnippet string    `json:"thinking_snippet,omitempty"`
	OutputSnippet   string    `json:"output_snippet,omitempty"`
	StopReason      string    `json:"stop_reason,omitempty"`
	UsageInput      int       `json:"usage_input,omitempty"`
	UsageOutput     int       `json:"usage_output,omitempty"`
	UsageCacheRead  int       `json:"usage_cache_read,omitempty"`
	UsageCacheWrite int       `json:"usage_cache_write,omitempty"`
	DurationMS      int64     `json:"duration_ms,omitempty"`
	ElapsedMS       int64     `json:"elapsed_ms,omitempty"`
	FirstTokenMS    int64     `json:"first_token_ms,omitempty"`
	Streaming       bool      `json:"streaming"`
	Attempt         int       `json:"attempt,omitempty"`
	Error           string    `json:"error,omitempty"`
	Message         string    `json:"message,omitempty"`
}

type Call struct {
	mu      sync.Mutex
	rec     Record
	started time.Time
}

type chatKey struct{}

type ChatScope struct {
	SessionID   string
	CharacterID string
}

func WithChat(ctx context.Context, sessionID, characterID string) context.Context {
	return context.WithValue(ctx, chatKey{}, ChatScope{SessionID: sessionID, CharacterID: characterID})
}

func ChatFrom(ctx context.Context) ChatScope {
	v, _ := ctx.Value(chatKey{}).(ChatScope)
	return v
}

func NewCallID() string {
	var raw [4]byte
	_, _ = rand.Read(raw[:])
	return fmt.Sprintf("%s-%s", time.Now().UTC().Format("150405.000"), hex.EncodeToString(raw[:]))
}

func NewCall(rec Record) *Call {
	if rec.CallID == "" {
		rec.CallID = NewCallID()
	}
	rec.Event = EventStart
	return &Call{rec: rec, started: time.Now()}
}

func (c *Call) Snapshot() Record {
	if c == nil {
		return Record{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.rec
	if !c.started.IsZero() {
		out.ElapsedMS = time.Since(c.started).Milliseconds()
	}
	return out
}

func (c *Call) Update(fn func(*Record)) {
	if c == nil || fn == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(&c.rec)
}

func (c *Call) ApplyResponse(resp *agentcore.LLMResponse) {
	c.Update(func(rec *Record) { ApplyResponse(rec, resp) })
}

func Start(sink Sink, call *Call) {
	if call == nil {
		return
	}
	rec := call.Snapshot()
	rec.Event = EventStart
	if rec.Streaming {
		rec.Message = "开始流式生成"
	} else {
		rec.Message = "开始非流式等待最终结果"
	}
	Write(sink, &rec)
}

func Finish(sink Sink, call *Call, err error) {
	if call == nil {
		return
	}
	rec := call.Snapshot()
	rec.Event = EventFinish
	rec.DurationMS = rec.ElapsedMS
	if err != nil {
		rec.Error = err.Error()
		if rec.Streaming {
			rec.Message = "流式调用失败"
		} else {
			rec.Message = "非流式调用失败"
		}
	} else if rec.Streaming {
		rec.Message = "流式调用结束"
	} else {
		rec.Message = "非流式调用结束"
	}
	Write(sink, &rec)
	if err == nil {
		logCacheHit(rec)
	}
}

func Heartbeat(sink Sink, call *Call, interval time.Duration) func() {
	if sink == nil || call == nil || interval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(finished)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				rec := call.Snapshot()
				rec.Event = EventHeartbeat
				if rec.Streaming {
					rec.Message = "流式调用仍在进行"
				} else {
					rec.Message = "仍在等待最终结果（非流式）"
				}
				Write(sink, &rec)
			}
		}
	}()
	return func() {
		once.Do(func() { close(done) })
		<-finished
	}
}

func MarkFirstToken(sink Sink, call *Call) {
	if call == nil {
		return
	}
	var rec Record
	call.Update(func(item *Record) {
		if item.FirstTokenMS > 0 {
			return
		}
		if !call.started.IsZero() {
			item.FirstTokenMS = time.Since(call.started).Milliseconds()
		}
		rec = *item
	})
	if rec.FirstTokenMS <= 0 {
		return
	}
	rec.Event = EventFirstToken
	rec.ElapsedMS = rec.FirstTokenMS
	rec.Message = "收到首个流式增量"
	Write(sink, &rec)
}

func Note(sink Sink, rec Record) {
	rec.Event = EventNote
	Write(sink, &rec)
}

func Retry(sink Sink, call *Call, attempt int, delay time.Duration, err error) {
	if call == nil {
		return
	}
	rec := call.Snapshot()
	rec.Event = EventRetry
	rec.Attempt = attempt
	if err != nil {
		rec.Error = err.Error()
	}
	rec.Message = fmt.Sprintf("请求重试 delay_ms=%d", delay.Milliseconds())
	Write(sink, &rec)
}

func Correction(sink Sink, call *Call, attempt int, layer, errText string, rawChars int) {
	if call == nil {
		return
	}
	call.Update(func(rec *Record) { rec.Attempt = attempt })
	rec := call.Snapshot()
	rec.Event = EventCorrect
	rec.Attempt = attempt
	rec.Error = snippet(errText, 160)
	rec.Message = fmt.Sprintf("输出纠偏 layer=%s raw_chars=%d", layer, rawChars)
	Write(sink, &rec)
}

func Repaired(sink Sink, call *Call, rules []string, rawChars, bodyChars int) {
	if call == nil {
		return
	}
	rec := call.Snapshot()
	rec.Event = EventRepair
	rec.Message = fmt.Sprintf("json repaired rules=%s raw_chars=%d body_chars=%d", strings.Join(rules, ","), rawChars, bodyChars)
	Write(sink, &rec)
}

func ApplyResponse(rec *Record, resp *agentcore.LLMResponse) {
	if rec == nil || resp == nil {
		return
	}
	text := resp.Message.TextContent()
	thinking := resp.Message.ThinkingContent()
	rec.StopReason = string(resp.Message.StopReason)
	rec.OutputChars = utf8.RuneCountInString(text)
	rec.ThinkingChars = utf8.RuneCountInString(thinking)
	rec.OutputSnippet = snippet(text, 80)
	rec.ThinkingSnippet = snippet(thinking, 80)
	if resp.Message.Usage != nil {
		rec.UsageInput = resp.Message.Usage.Input
		rec.UsageOutput = resp.Message.Usage.Output
		rec.UsageCacheRead = resp.Message.Usage.CacheRead
		rec.UsageCacheWrite = resp.Message.Usage.CacheWrite
		if rec.Provider == "" {
			rec.Provider = resp.Message.Usage.Provider
		}
		if rec.Model == "" {
			rec.Model = resp.Message.Usage.Model
		}
	}
}

func Write(sink Sink, rec *Record) {
	if sink == nil || rec == nil {
		return
	}
	if rec.TS.IsZero() {
		rec.TS = time.Now().UTC()
	} else {
		rec.TS = rec.TS.UTC()
	}
	line := formatLine(*rec)
	_ = sink.AppendText(IndexRel, line)
	if runtimeRel, ok := scopedRel(*rec, "runtime.log"); ok {
		_ = sink.AppendText(runtimeRel, line)
	}
	if rec.Event != EventNote {
		if callsRel, ok := scopedRel(*rec, "calls.jsonl"); ok {
			_ = sink.AppendJSONL(callsRel, rec)
		}
	}
}

func cacheHit(rec Record) (int, int, float64) {
	hit := rec.UsageCacheRead
	miss := rec.UsageInput - hit
	if miss < 0 {
		miss = 0
	}
	if rec.UsageInput <= 0 {
		return hit, miss, 0
	}
	return hit, miss, float64(hit) / float64(rec.UsageInput) * 100
}

func logCacheHit(rec Record) {
	if rec.UsageInput <= 0 && rec.UsageCacheRead <= 0 {
		slog.Warn("galgame 未返回 usage，无法判断缓存命中",
			"module", "galgame", "mode", rec.Mode, "step", rec.Step,
			"session", rec.SessionID, "play", rec.PlayID, "call", rec.CallID,
			"model", rec.Provider+"/"+rec.Model)
		return
	}
	hit, miss, rate := cacheHit(rec)
	slog.Info("galgame 缓存命中",
		"module", "galgame", "mode", rec.Mode, "step", rec.Step,
		"session", rec.SessionID, "play", rec.PlayID, "call", rec.CallID,
		"model", rec.Provider+"/"+rec.Model,
		"input", rec.UsageInput, "output", rec.UsageOutput,
		"cache_hit", hit, "cache_miss", miss,
		"cache_rate", fmt.Sprintf("%.1f%%", rate))
}

func PromptChars(msgs []agentcore.Message) int {
	n := 0
	for _, msg := range msgs {
		n += utf8.RuneCountInString(msg.TextContent())
	}
	return n
}

func scopedRel(rec Record, name string) (string, bool) {
	switch rec.Mode {
	case ModeChat:
		if !safeID(rec.SessionID) {
			return "", false
		}
		return filepath.ToSlash(filepath.Join("galgame/sessions", rec.SessionID, name)), true
	case ModePlay:
		if !safeID(rec.PlayID) {
			return "", false
		}
		return filepath.ToSlash(filepath.Join("galgame/plays", rec.PlayID, name)), true
	default:
		return "", false
	}
}

func safeID(id string) bool {
	id = strings.TrimSpace(id)
	return id != "" && filepath.Base(id) == id && !strings.ContainsAny(id, `/\\`)
}

func formatLine(rec Record) string {
	parts := []string{
		rec.TS.Format("2006-01-02T15:04:05.000Z07:00"),
		strings.ToUpper(rec.Event),
		rec.Mode,
	}
	if rec.Step != "" {
		parts = append(parts, rec.Step)
	}
	if rec.CallID != "" {
		parts = append(parts, "call="+rec.CallID)
	}
	if rec.SessionID != "" {
		parts = append(parts, "session="+rec.SessionID)
	}
	if rec.PlayID != "" {
		parts = append(parts, "play="+rec.PlayID)
	}
	if rec.Provider != "" || rec.Model != "" {
		parts = append(parts, "model="+rec.Provider+"/"+rec.Model)
	}
	if rec.Thinking != "" {
		parts = append(parts, "thinking="+rec.Thinking)
	}
	if rec.Protocol != "" {
		parts = append(parts, "protocol="+rec.Protocol)
	}
	if rec.MaxTokens > 0 {
		parts = append(parts, fmt.Sprintf("max_tokens=%d", rec.MaxTokens))
	}
	if rec.PromptChars > 0 {
		parts = append(parts, fmt.Sprintf("prompt_chars=%d", rec.PromptChars))
	}
	if rec.ThinkingChars > 0 {
		parts = append(parts, fmt.Sprintf("thinking_chars=%d", rec.ThinkingChars))
	}
	if rec.OutputChars > 0 {
		parts = append(parts, fmt.Sprintf("output_chars=%d", rec.OutputChars))
	}
	if rec.StopReason != "" {
		parts = append(parts, "stop="+rec.StopReason)
	}
	if rec.UsageInput > 0 || rec.UsageOutput > 0 || rec.UsageCacheRead > 0 {
		hit, miss, rate := cacheHit(rec)
		parts = append(parts, fmt.Sprintf("usage_in=%d usage_out=%d cache_hit=%d cache_miss=%d cache_rate=%.1f%%", rec.UsageInput, rec.UsageOutput, hit, miss, rate))
	}
	if rec.DurationMS > 0 {
		parts = append(parts, fmt.Sprintf("duration_ms=%d", rec.DurationMS))
	} else if rec.ElapsedMS > 0 {
		parts = append(parts, fmt.Sprintf("elapsed_ms=%d", rec.ElapsedMS))
	}
	if rec.FirstTokenMS > 0 {
		parts = append(parts, fmt.Sprintf("first_token_ms=%d", rec.FirstTokenMS))
	}
	parts = append(parts, fmt.Sprintf("streaming=%t", rec.Streaming))
	if rec.Message != "" {
		parts = append(parts, rec.Message)
	}
	if rec.Error != "" {
		parts = append(parts, "err="+snippet(rec.Error, 160))
	}
	return strings.Join(parts, "  ")
}

func snippet(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxRunes <= 0 {
		return ""
	}
	var b strings.Builder
	lastSpace := false
	for _, r := range value {
		if unicode.IsSpace(r) {
			if lastSpace {
				continue
			}
			r = ' '
			lastSpace = true
		} else {
			lastSpace = false
		}
		b.WriteRune(r)
	}
	runes := []rune(strings.TrimSpace(b.String()))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "…"
}
