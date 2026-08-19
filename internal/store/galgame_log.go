package store

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// tavernLogCapBytes 是单个活跃日志文件的大小上限，超限时轮转为同名 .old。
// 剧场 stream.log 记录全部思考/输出增量，不设上限会"局越长读写越卡"。
// var 而非 const：测试内临时调小。
var tavernLogCapBytes int64 = 8 << 20

// TavernLogObserver 在酒馆/剧场日志追加成功后被回调。
// rel 是日志路径；epoch 是文件代际（每次轮转 +1）；off 是本次追加前的文件偏移；
// data 是本次追加内容。实现必须非阻塞——回调在写路径持锁时发起。
type TavernLogObserver func(rel string, epoch int, off int64, data []byte)

// tavernLogState 跟踪单个日志文件的活跃大小与代际。
// 轮转判定、observer 的 off 记账和 SSE 快照去重都以此为事实源。
type tavernLogState struct {
	size  int64
	epoch int
}

func (s *GalgameStore) AppendJSONL(rel string, v any) error {
	if s == nil || s.io == nil {
		return nil
	}
	if !safeGalgameRel(rel) {
		return fmt.Errorf("invalid tavern log path")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.appendTavernLog(rel, append(data, '\n'), true)
}

func (s *GalgameStore) AppendText(rel string, line string) error {
	if s == nil || s.io == nil {
		return nil
	}
	if !safeGalgameRel(rel) {
		return fmt.Errorf("invalid tavern log path")
	}
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	return s.appendTavernLog(rel, []byte(line), true)
}

func (s *GalgameStore) AppendRaw(rel string, data string) error {
	if s == nil || s.io == nil || data == "" {
		return nil
	}
	if !safeGalgameRel(rel) {
		return fmt.Errorf("invalid tavern log path")
	}
	return s.appendTavernLog(rel, []byte(data), false)
}

// SetLogObserver 安装日志追加回调（SSE 推送的内存分发层）。nil 清除。
func (s *GalgameStore) SetLogObserver(fn TavernLogObserver) {
	if s == nil {
		return
	}
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.logObserver = fn
}

// appendTavernLog 是三类追加的统一路径：轮转判定 → 追加 → 记账 → observer。
// synced 决定是否 fsync（结构化记录要，高频流式增量不要）。
func (s *GalgameStore) appendTavernLog(rel string, data []byte, synced bool) error {
	if len(data) == 0 {
		return nil
	}
	s.logMu.Lock()
	defer s.logMu.Unlock()
	st := s.logStateLocked(rel)
	if st.size > 0 && st.size+int64(len(data)) > tavernLogCapBytes {
		s.rotateLocked(rel, st)
	}
	off := st.size
	var err error
	if synced {
		err = s.io.AppendLine(rel, data)
	} else {
		err = s.io.AppendBytes(rel, data)
	}
	if err != nil {
		return err
	}
	st.size += int64(len(data))
	if s.logObserver != nil {
		s.logObserver(rel, st.epoch, off, data)
	}
	return nil
}

// logStateLocked 取/建文件状态；首次访问用 stat 初始化大小（断点续写兼容）。
func (s *GalgameStore) logStateLocked(rel string) *tavernLogState {
	if s.logState == nil {
		s.logState = map[string]*tavernLogState{}
	}
	if st, ok := s.logState[rel]; ok {
		return st
	}
	st := &tavernLogState{}
	if fi, err := os.Stat(s.io.path(rel)); err == nil {
		st.size = fi.Size()
	}
	s.logState[rel] = st
	return st
}

// rotateLocked 轮转活跃日志：rename 为 .old（覆盖更早的轮转件，只保留上一代）。
// rename 失败以截断兜底，保证活跃文件大小有界。调用方持有 logMu。
func (s *GalgameStore) rotateLocked(rel string, st *tavernLogState) {
	p := s.io.path(rel)
	if err := os.Rename(p, p+".old"); err != nil {
		_ = os.Truncate(p, 0)
	}
	st.size = 0
	st.epoch++
}

// ReadLogTail 返回日志尾部的可读文本与读取时刻的文件位置（epoch + 末尾偏移）。
// 读取在 logMu 下与追加/轮转互斥，SSE"先订阅再快照"据此做不漏不重去重：
// 快照后到达的增量满足 epoch 更大，或同代且 off >= 末尾偏移。
// 返回文本保证从合法 UTF-8 边界开始；文件不存在返回空文本与当前位置。
func (s *GalgameStore) ReadLogTail(rel string, max int) (string, int, int64, error) {
	if s == nil || s.io == nil {
		return "", 0, 0, nil
	}
	if !safeGalgameRel(rel) {
		return "", 0, 0, fmt.Errorf("invalid tavern log path")
	}
	s.logMu.Lock()
	defer s.logMu.Unlock()
	st := s.logStateLocked(rel)
	f, err := os.Open(s.io.path(rel))
	if err != nil {
		if os.IsNotExist(err) {
			return "", st.epoch, st.size, nil
		}
		return "", 0, 0, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return "", 0, 0, err
	}
	size := fi.Size()
	start := int64(0)
	if max > 0 && size > int64(max) {
		start = size - int64(max)
	}
	data, err := io.ReadAll(io.NewSectionReader(f, start, size-start))
	if err != nil {
		return "", 0, 0, err
	}
	cut := 0
	for cut < len(data) && cut < utf8.UTFMax && !utf8.Valid(data[cut:]) {
		cut++
	}
	return string(data[cut:]), st.epoch, st.size, nil
}

func (s *GalgameStore) ReadText(rel string) (string, error) {
	if s == nil || s.io == nil {
		return "", nil
	}
	if !safeGalgameRel(rel) {
		return "", fmt.Errorf("invalid tavern log path")
	}
	data, err := s.io.ReadFile(rel)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func safeGalgameRel(rel string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" || strings.Contains(rel, "..") {
		return false
	}
	return strings.HasPrefix(rel, "galgame/")
}
