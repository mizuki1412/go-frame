package sessionstore

import (
	"fmt"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/service/configkit"
)

// PerfData 会话性能数据：perf.json 的顶层就是一个调用事件数组，
// 会话内每一次模型调用、每一次工具调用各占一个元素，按发生顺序排列。
// 每个元素写明调用对象（模型名 + 回答类型 / 工具名）、开始与结束时刻、
// 以及开始到结束的耗时，可直接按时间线复盘一次会话。
type PerfData []PerfEvent

const (
	// PerfTypeLLM 模型调用
	PerfTypeLLM = "llm"
	// PerfTypeTool 工具调用
	PerfTypeTool = "tool"

	// PerfModeThinking 模型深度思考（返回了推理过程或推理 token）
	PerfModeThinking = "thinking"
	// PerfModeDirect 模型直接回答（无推理过程）
	PerfModeDirect = "direct"
)

// PerfEvent 一次调用（模型或工具）的性能记录，perf.json 数组的单个元素
type PerfEvent struct {
	Seq        int      `json:"seq"`                  // 会话内调用序号，从 1 递增
	Type       string   `json:"type"`                 // llm / tool
	Model      string   `json:"model,omitempty"`      // type=llm：调用的模型名
	Mode       string   `json:"mode,omitempty"`       // type=llm：thinking（深度思考）/ direct（直接回答）
	Tool       string   `json:"tool,omitempty"`       // type=tool：调用的工具名
	ToolCallID string   `json:"toolCallId,omitempty"` // type=tool：对应的 tool call ID
	ToolCalls  []string `json:"toolCalls,omitempty"`  // type=llm：本次调用发起的工具调用名

	StartedAt   string `json:"startedAt"`  // 调用开始时刻
	EndedAt     string `json:"endedAt"`    // 调用结束时刻
	DurationMs  int64  `json:"durationMs"` // 开始到结束的耗时（ms）
	Interrupted bool   `json:"interrupted,omitempty"`

	PromptTokens     int `json:"promptTokens,omitempty"`
	CompletionTokens int `json:"completionTokens,omitempty"`
	TotalTokens      int `json:"totalTokens,omitempty"`
	ReasoningTokens  int `json:"reasoningTokens,omitempty"`
}

// 会话性能数据：与 history.json 同目录，路径 <sessionDir>/<id>/perf.json。
// PerfRecorder 在 Session.Run/Resume 的事件循环中记录：
//   - LLM 调用：等待模型响应的起点（Run/Resume 开始、或工具结果返回）→ 助手消息到达，
//     记录模型名、回答类型（深度思考 / 直接回答）、耗时与 token 用量；
//   - 工具调用：助手消息发起 tool call → 对应 tool 结果消息返回，记录工具名与耗时
//     （ReAct 循环内多个工具并发时，墙钟上各工具的区间可能重叠，各自单独保留）。
//
// 数据跨 Resume、跨进程重启累计：恢复历史会话（LoadHistory）时载入已有数组继续追加。
type PerfRecorder struct {
	store        *SessionStore
	id           string
	model        string
	llmStart     time.Time              // 最近一次"等待模型响应"的起点（零值表示不在计时中）
	pendingTools map[string]toolPending // toolCallID -> 工具名与发起时刻
	nextSeq      int                    // 下一个事件的会话内序号
	data         PerfData
}

// toolPending 已发起但结果尚未返回的工具调用
type toolPending struct {
	name  string
	start time.Time
}

// NewPerfRecorder 创建性能记录器；store 为 nil 时为空实现（不落盘）。
// 恢复历史会话时载入已有调用数组，序号接续递增。
func NewPerfRecorder(store *SessionStore, sessionID string) *PerfRecorder {
	r := &PerfRecorder{
		store:        store,
		id:           sessionID,
		model:        configkit.GetString(configkey.LLMModel),
		pendingTools: make(map[string]toolPending),
		nextSeq:      1,
	}
	if store != nil {
		if p, err := store.LoadPerf(sessionID); err == nil && p != nil {
			r.data = p
			for _, e := range p {
				if e.Seq >= r.nextSeq {
					r.nextSeq = e.Seq + 1
				}
			}
		}
	}
	return r
}

// LLMStart 标记一次"等待模型响应"的起点（Run/Resume 开始、工具结果返回后调用）。
// 若已有未结算的计时（同轮多个工具结果依次返回）则不重置起点
func (r *PerfRecorder) LLMStart() {
	if r == nil || !r.llmStart.IsZero() {
		return
	}
	r.llmStart = time.Now()
}

// RecordAssistant 记录助手消息：若正在等待模型响应则结算一次 LLM 调用，
// 并把消息中的工具调用记入 pending（发起时刻=当前）
func (r *PerfRecorder) RecordAssistant(m *schema.Message) {
	if r == nil || m == nil {
		return
	}
	if !r.llmStart.IsZero() {
		r.finishLLMCall(m, time.Now())
	}
	now := time.Now()
	for _, tc := range m.ToolCalls {
		if tc.ID != "" {
			r.pendingTools[tc.ID] = toolPending{name: tc.Function.Name, start: now}
		}
	}
}

// finishLLMCall 结算一次 LLM 调用：记录模型名、回答类型、开始/结束时刻、耗时与 token 用量
func (r *PerfRecorder) finishLLMCall(m *schema.Message, now time.Time) {
	start := r.llmStart
	if start.IsZero() {
		start = now
	}
	r.llmStart = time.Time{}

	e := PerfEvent{
		Type:       PerfTypeLLM,
		Model:      r.model,
		Mode:       llmMode(m),
		StartedAt:  start.Format(time.RFC3339Nano),
		EndedAt:    now.Format(time.RFC3339Nano),
		DurationMs: now.Sub(start).Milliseconds(),
	}
	if m != nil && m.ResponseMeta != nil && m.ResponseMeta.Usage != nil {
		u := m.ResponseMeta.Usage
		e.PromptTokens = u.PromptTokens
		e.CompletionTokens = u.CompletionTokens
		e.TotalTokens = u.TotalTokens
		e.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	if m != nil {
		for _, tc := range m.ToolCalls {
			e.ToolCalls = append(e.ToolCalls, tc.Function.Name)
		}
	}
	r.appendEvent(e)
}

// RecordToolResult 记录工具结果消息：结算对应工具调用的耗时，并标记下一段等待模型响应
func (r *PerfRecorder) RecordToolResult(m *schema.Message) {
	if r == nil || m == nil {
		return
	}
	if m.ToolCallID != "" {
		if p, ok := r.pendingTools[m.ToolCallID]; ok {
			now := time.Now()
			delete(r.pendingTools, m.ToolCallID)
			name := p.name
			if name == "" {
				name = m.ToolName
			}
			r.appendEvent(PerfEvent{
				Type:       PerfTypeTool,
				Tool:       name,
				ToolCallID: m.ToolCallID,
				StartedAt:  p.start.Format(time.RFC3339Nano),
				EndedAt:    now.Format(time.RFC3339Nano),
				DurationMs: now.Sub(p.start).Milliseconds(),
			})
		}
	}
	// 工具结果返回后，agent 即将再次请求模型
	r.LLMStart()
}

// Interrupt 结算未完成的调用（本轮被中断时调用）：已发起但结果未返回的工具调用、
// 以及进行中的模型调用按中断时刻截止，并标记 interrupted。
// 否则进行中的计时会一直挂到下次 Resume，把用户中断期间的空闲时间算进耗时。
func (r *PerfRecorder) Interrupt() {
	if r == nil {
		return
	}
	now := time.Now()
	for id, p := range r.pendingTools {
		delete(r.pendingTools, id)
		r.appendEvent(PerfEvent{
			Type:        PerfTypeTool,
			Tool:        p.name,
			ToolCallID:  id,
			StartedAt:   p.start.Format(time.RFC3339Nano),
			EndedAt:     now.Format(time.RFC3339Nano),
			DurationMs:  now.Sub(p.start).Milliseconds(),
			Interrupted: true,
		})
	}
	if !r.llmStart.IsZero() {
		start := r.llmStart
		r.llmStart = time.Time{}
		r.appendEvent(PerfEvent{
			Type:        PerfTypeLLM,
			Model:       r.model,
			StartedAt:   start.Format(time.RFC3339Nano),
			EndedAt:     now.Format(time.RFC3339Nano),
			DurationMs:  now.Sub(start).Milliseconds(),
			Interrupted: true,
		})
	}
}

// appendEvent 追加一个调用事件并分配会话内序号
func (r *PerfRecorder) appendEvent(e PerfEvent) {
	e.Seq = r.nextSeq
	r.nextSeq++
	r.data = append(r.data, e)
}

// llmMode 判定本次模型调用的回答类型：有推理过程或推理 token 即为深度思考
func llmMode(m *schema.Message) string {
	if m != nil && m.ReasoningContent != "" {
		return PerfModeThinking
	}
	if m != nil && m.ResponseMeta != nil && m.ResponseMeta.Usage != nil &&
		m.ResponseMeta.Usage.CompletionTokensDetails.ReasoningTokens > 0 {
		return PerfModeThinking
	}
	return PerfModeDirect
}

// ID 返回本记录器对应的会话 ID
func (r *PerfRecorder) ID() string {
	if r == nil {
		return ""
	}
	return r.id
}

// Save 将调用事件数组原子写入 <sessionDir>/<id>/perf.json；store 为 nil 时为空操作
func (r *PerfRecorder) Save() error {
	if r == nil || r.store == nil {
		return nil
	}
	return r.store.SavePerf(r.id, r.data)
}

// Load 从磁盘加载本记录器对应会话的调用事件数组（不存在时为 nil）
func (r *PerfRecorder) Load() PerfData {
	if r == nil || r.store == nil {
		return nil
	}
	p, _ := r.store.LoadPerf(r.id)
	return p
}

// Summary 从调用事件数组汇总出人读摘要（未启用或无任何数据时为空串）
func (r *PerfRecorder) Summary() string {
	if r == nil || len(r.data) == 0 {
		return ""
	}
	var llm, thinking, tool int
	var llmMs, toolMs, tokens int64
	for _, e := range r.data {
		switch e.Type {
		case PerfTypeLLM:
			llm++
			llmMs += e.DurationMs
			tokens += int64(e.TotalTokens)
			if e.Mode == PerfModeThinking {
				thinking++
			}
		case PerfTypeTool:
			tool++
			toolMs += e.DurationMs
		}
	}
	s := fmt.Sprintf("LLM %d 次 / %s，工具 %d 次 / %s", llm, fmtMs(llmMs), tool, fmtMs(toolMs))
	if thinking > 0 {
		s += fmt.Sprintf("（深度思考 %d 次）", thinking)
	}
	if tokens > 0 {
		s += fmt.Sprintf("，tokens %d", tokens)
	}
	return s
}

// fmtMs 把毫秒数格式化为 time.Duration 人读形式（如 12.3s）
func fmtMs(ms int64) string {
	return (time.Duration(ms) * time.Millisecond).String()
}
