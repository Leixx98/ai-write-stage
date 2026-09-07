package host

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Leixx98/ai-write-stage/internal/galgame/runlog"
)

// playLogTailBytes 是日志快照的尾部长上限。面板只看最近内容，全量没有意义。
const playLogTailBytes = 400 * 1024

// playLogClientBuffer 是单个 SSE 订阅者的增量缓冲。满则丢：文件是事实源，
// 前端重连重拉快照即可补齐——与 eventBroker 的丢弃语义一致。
const playLogClientBuffer = 512

// PlayLogItem 是按剧场路由后的日志增量，供 SSE 端点消费。
type PlayLogItem struct {
	Kind  string // events / stream
	Epoch int    // 文件代际（轮转 +1）
	Off   int64  // 追加前文件偏移
	Text  string
}

// PlayLog 是面板展示的尾部快照（REST /log 的载荷）。
type PlayLog struct {
	Events string `json:"events"`
	Stream string `json:"stream"`
}

// PlayLogSnapshot 在 PlayLog 之上附带读取时刻的文件位置，
// SSE"先订阅再快照"用 epoch/off 做不漏不重的增量过滤。
type PlayLogSnapshot struct {
	PlayLog
	EventsEpoch int
	EventsEnd   int64
	StreamEpoch int
	StreamEnd   int64
}

// playLogBroker 把酒馆 store 的日志追加按剧场 ID 分发给 SSE 订阅者。
type playLogBroker struct {
	mu      sync.Mutex
	nextID  int
	clients map[int]playLogClient
}

type playLogClient struct {
	playID string
	ch     chan PlayLogItem
}

func newPlayLogBroker() *playLogBroker {
	return &playLogBroker{clients: map[int]playLogClient{}}
}

// observe 实现 store.TavernLogObserver；只路由剧场面板关心的两个文件。
func (b *playLogBroker) observe(rel string, epoch int, off int64, data []byte) {
	if b == nil {
		return
	}
	playID, kind, ok := parsePlayLogRel(rel)
	if !ok || len(data) == 0 {
		return
	}
	item := PlayLogItem{Kind: kind, Epoch: epoch, Off: off, Text: string(data)}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, client := range b.clients {
		if client.playID != playID {
			continue
		}
		select {
		case client.ch <- item:
		default:
		}
	}
}

func (b *playLogBroker) subscribe(playID string) (int, <-chan PlayLogItem) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	ch := make(chan PlayLogItem, playLogClientBuffer)
	b.clients[id] = playLogClient{playID: playID, ch: ch}
	return id, ch
}

func (b *playLogBroker) unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.clients, id)
}

// parsePlayLogRel 把酒馆日志路径映射为（剧场 ID, 面板频道）。
// runtime.log → 事件面板；stream.log → 模型回复面板；其余文件不广播。
func parsePlayLogRel(rel string) (string, string, bool) {
	rel = filepath.ToSlash(rel)
	rest, ok := strings.CutPrefix(rel, "galgame/plays/")
	if !ok {
		return "", "", false
	}
	id, name, ok := strings.Cut(rest, "/")
	if !ok || id == "" {
		return "", "", false
	}
	switch name {
	case "runtime.log":
		return id, "events", true
	case runlog.StreamFile:
		return id, "stream", true
	}
	return "", "", false
}

// SubscribePlayLog 订阅某剧场日志增量；返回频道与释放函数。
// Host 未就绪时返回已关闭频道，调用方无需判空。
func (h *Host) SubscribePlayLog(playID string) (<-chan PlayLogItem, func()) {
	if h == nil || h.playLog == nil {
		ch := make(chan PlayLogItem)
		close(ch)
		return ch, func() {}
	}
	id, ch := h.playLog.subscribe(playID)
	return ch, func() { h.playLog.unsubscribe(id) }
}

// PlayLogSnapshot 读取剧场日志尾部快照与文件位置。先校验剧场存在；
// 位置与内容在同一把锁内取得（ReadLogTail），与 observer 增量严格对齐。
func (h *Host) PlayLogSnapshot(id string) (PlayLogSnapshot, error) {
	if h == nil || h.roots == nil || h.roots.Tavern == nil {
		return PlayLogSnapshot{}, fmt.Errorf("tavern store is unavailable")
	}
	if _, err := h.roots.Tavern.LoadPlay(id); err != nil {
		return PlayLogSnapshot{}, err
	}
	eventsRel, ok := runlog.PlayFile(id, "runtime.log")
	if !ok {
		return PlayLogSnapshot{}, fmt.Errorf("invalid play id")
	}
	streamRel, _ := runlog.PlayFile(id, runlog.StreamFile)
	events, eventsEpoch, eventsEnd, err := h.roots.Tavern.ReadLogTail(eventsRel, playLogTailBytes)
	if err != nil {
		return PlayLogSnapshot{}, err
	}
	stream, streamEpoch, streamEnd, err := h.roots.Tavern.ReadLogTail(streamRel, playLogTailBytes)
	if err != nil {
		return PlayLogSnapshot{}, err
	}
	return PlayLogSnapshot{
		PlayLog:     PlayLog{Events: events, Stream: stream},
		EventsEpoch: eventsEpoch,
		EventsEnd:   eventsEnd,
		StreamEpoch: streamEpoch,
		StreamEnd:   streamEnd,
	}, nil
}

// PlayLog 保留 REST 快照语义（诊断/降级用）。
func (h *Host) PlayLog(id string) (PlayLog, error) {
	snap, err := h.PlayLogSnapshot(id)
	return snap.PlayLog, err
}
