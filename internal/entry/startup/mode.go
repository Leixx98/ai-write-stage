package startup

import "fmt"

// The startup package coordinates preparation before entering the Engine.
// Entry points collect input, startup builds quick/co-create/resume plans, and
// the Engine only executes prepared sessions.

// Mode 表示进入 Engine 之前的启动策略类型。
type Mode string

const (
	// ModeQuick 直接以用户输入作为创作起点。
	ModeQuick Mode = "quick"
	// ModeCoCreate 先做多轮澄清，再产出创作草稿进入 Engine。
	ModeCoCreate Mode = "cocreate"
	// ModeContinueFromNovel 基于已有小说内容装配上下文后续写。
	ModeContinueFromNovel Mode = "continue_from_novel"
)

// Request 描述入口层提交给启动策略层的原始输入。
// 宿主入口先收集用户输入，再由 startup 把它整理为可进入 Engine 的计划。
type Request struct {
	Mode        Mode
	UserPrompt  string
	NovelPath   string
	OutputDir   string
	Interactive bool
}

// Plan 描述启动策略层产出的结果。
// 宿主入口不应自己拼接正式启动 prompt，而应消费 Plan 再驱动 Engine。
type Plan struct {
	Mode        Mode
	DisplayName string
	RawPrompt   string // 用户原始创作要求（未包装）；供用户规则归一化使用，resume 模式为空
	ResumeOnly  bool
}

// ErrNotImplemented 标记占位策略尚未落地。
var ErrNotImplemented = fmt.Errorf("startup mode not implemented")

// PrepareContinueFromNovel is the shared extension point for continuing an existing novel.
// Every entry point must normalize input into a Request before building a Plan here.
func PrepareContinueFromNovel(req Request) (Plan, error) {
	return Plan{}, fmt.Errorf("%w: %s", ErrNotImplemented, ModeContinueFromNovel)
}
