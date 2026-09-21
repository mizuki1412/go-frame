package sessionstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/service/configkit"
)

// newTestStore 在临时目录创建会话存储，并固定 llm.model 便于断言记录出的模型名
func newTestStore(t *testing.T) *SessionStore {
	t.Helper()
	configkit.Set(configkey.LLMModel, "deepseek-v4")
	t.Cleanup(func() { configkit.Set(configkey.LLMModel, "") })
	s, err := NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// toolCallMsg 构造带单个工具调用的助手消息
func toolCallMsg(id, name string) *schema.Message {
	return &schema.Message{
		Role:      schema.Assistant,
		ToolCalls: []schema.ToolCall{{ID: id, Function: schema.FunctionCall{Name: name}}},
	}
}

func assertEvent(t *testing.T, e PerfEvent, typ, what string) {
	t.Helper()
	if e.Seq <= 0 {
		t.Errorf("seq 未分配: %+v", e)
	}
	if e.Type != typ {
		t.Errorf("%s: type = %q, want %q", what, e.Type, typ)
	}
	if e.DurationMs < 0 {
		t.Errorf("%s: durationMs = %d, want >= 0", what, e.DurationMs)
	}
	if e.StartedAt == "" || e.EndedAt == "" {
		t.Errorf("%s: 缺开始/结束时刻: %+v", what, e)
	} else if st, err := time.Parse(time.RFC3339Nano, e.StartedAt); err == nil {
		if en, err := time.Parse(time.RFC3339Nano, e.EndedAt); err == nil && en.Before(st) {
			t.Errorf("%s: endedAt 早于 startedAt: %+v", what, e)
		}
	}
}

// 一轮 ReAct：模型深度思考并发起两个工具调用 → 工具逐个返回 → 模型直接回答。
// perf.json 应为调用事件数组，每个元素一次调用，按发生顺序排列。
func TestPerfRecorderWritesCallTimeline(t *testing.T) {
	s := newTestStore(t)
	r := NewPerfRecorder(s, "s1")
	if r.model != "deepseek-v4" {
		t.Fatalf("model = %q", r.model)
	}

	r.LLMStart()
	time.Sleep(5 * time.Millisecond)
	r.RecordAssistant(&schema.Message{
		Role:             schema.Assistant,
		ReasoningContent: "先检索知识库再看本地文件",
		ToolCalls: []schema.ToolCall{
			{ID: "c1", Function: schema.FunctionCall{Name: "rag_search"}},
			{ID: "c2", Function: schema.FunctionCall{Name: "read_file"}},
		},
		ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{
			PromptTokens: 100, CompletionTokens: 30, TotalTokens: 130,
			CompletionTokensDetails: schema.CompletionTokensDetails{ReasoningTokens: 18},
		}},
	})
	time.Sleep(5 * time.Millisecond)
	r.RecordToolResult(&schema.Message{Role: schema.Tool, ToolCallID: "c1", ToolName: "rag_search"})
	time.Sleep(5 * time.Millisecond)
	r.RecordToolResult(&schema.Message{Role: schema.Tool, ToolCallID: "c2", ToolName: "read_file"})
	time.Sleep(5 * time.Millisecond)
	r.RecordAssistant(&schema.Message{
		Role:    schema.Assistant,
		Content: "答案",
		ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{
			PromptTokens: 200, CompletionTokens: 40, TotalTokens: 240,
		}},
	})
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	p, err := s.LoadPerf("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 4 {
		t.Fatalf("事件数 = %d, want 4: %+v", len(p), p)
	}

	assertEvent(t, p[0], PerfTypeLLM, "首个 LLM 调用")
	if p[0].Model != "deepseek-v4" {
		t.Errorf("model = %q", p[0].Model)
	}
	if p[0].Mode != PerfModeThinking {
		t.Errorf("mode = %q, want thinking", p[0].Mode)
	}
	if len(p[0].ToolCalls) != 2 || p[0].ToolCalls[0] != "rag_search" || p[0].ToolCalls[1] != "read_file" {
		t.Errorf("toolCalls = %v", p[0].ToolCalls)
	}
	if p[0].PromptTokens != 100 || p[0].TotalTokens != 130 || p[0].ReasoningTokens != 18 {
		t.Errorf("tokens = %+v", p[0])
	}

	assertEvent(t, p[1], PerfTypeTool, "工具调用 1")
	if p[1].Tool != "rag_search" || p[1].ToolCallID != "c1" {
		t.Errorf("工具 = %q / %q", p[1].Tool, p[1].ToolCallID)
	}
	assertEvent(t, p[2], PerfTypeTool, "工具调用 2")
	if p[2].Tool != "read_file" || p[2].ToolCallID != "c2" {
		t.Errorf("工具 = %q / %q", p[2].Tool, p[2].ToolCallID)
	}

	assertEvent(t, p[3], PerfTypeLLM, "末次 LLM 调用")
	if p[3].Mode != PerfModeDirect {
		t.Errorf("mode = %q, want direct", p[3].Mode)
	}
	if len(p[3].ToolCalls) != 0 {
		t.Errorf("toolCalls = %v, want empty", p[3].ToolCalls)
	}

	for i, e := range p {
		if want := i + 1; e.Seq != want {
			t.Errorf("seq[%d] = %d, want %d", i, e.Seq, want)
		}
	}

	// 磁盘上的 perf.json 顶层必须是数组
	data, err := os.ReadFile(filepath.Join(s.dir, "s1", "perf.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []PerfEvent
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("perf.json 不是数组: %v\n%s", err, data)
	}
	if len(onDisk) != 4 {
		t.Fatalf("落盘事件数 = %d, want 4", len(onDisk))
	}
}

// 恢复历史会话后序号接续递增，已有事件不被覆盖
func TestPerfRecorderResumesSeq(t *testing.T) {
	s := newTestStore(t)
	first := NewPerfRecorder(s, "s2")
	first.LLMStart()
	first.RecordAssistant(&schema.Message{Role: schema.Assistant, Content: "第一轮"})
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}

	restored := NewPerfRecorder(s, "s2")
	if len(restored.data) != 1 {
		t.Fatalf("载入事件数 = %d, want 1", len(restored.data))
	}
	restored.LLMStart()
	restored.RecordAssistant(toolCallMsg("c9", "execute"))
	restored.RecordToolResult(&schema.Message{Role: schema.Tool, ToolCallID: "c9", ToolName: "execute"})

	if len(restored.data) != 3 {
		t.Fatalf("事件数 = %d, want 3", len(restored.data))
	}
	for i, e := range restored.data {
		if e.Seq != i+1 {
			t.Errorf("seq[%d] = %d, want %d", i, e.Seq, i+1)
		}
	}
}

// 中断时结算进行中的调用：工具调用与模型调用按中断时刻截止并标记 interrupted，
// 计时归零，不会把用户中断期间的空闲时间算进耗时
func TestPerfRecorderInterrupt(t *testing.T) {
	configkit.Set(configkey.LLMModel, "m")

	// 工具执行中被中断
	r := NewPerfRecorder(nil, "s3")
	r.LLMStart()
	r.RecordAssistant(toolCallMsg("c1", "execute"))
	r.Interrupt()
	if len(r.data) != 2 {
		t.Fatalf("事件数 = %d, want 2: %+v", len(r.data), r.data)
	}
	if r.data[1].Type != PerfTypeTool || r.data[1].Tool != "execute" || !r.data[1].Interrupted {
		t.Errorf("中断的工具事件 = %+v", r.data[1])
	}
	if !r.llmStart.IsZero() || len(r.pendingTools) != 0 {
		t.Errorf("中断后计时/pending 未清理: llmStart=%v pending=%v", r.llmStart, r.pendingTools)
	}

	// 模型调用中被中断
	r2 := NewPerfRecorder(nil, "s4")
	r2.LLMStart()
	r2.Interrupt()
	if len(r2.data) != 1 || r2.data[0].Type != PerfTypeLLM || !r2.data[0].Interrupted {
		t.Fatalf("中断的 LLM 事件 = %+v", r2.data)
	}
	if !r2.llmStart.IsZero() {
		t.Error("中断后计时未清零，下次 LLMStart 不会重新起算")
	}
}

// 回答类型判定：有推理过程或推理 token 即为深度思考
func TestLLMMode(t *testing.T) {
	cases := []struct {
		name string
		msg  *schema.Message
		want string
	}{
		{"有推理过程", &schema.Message{ReasoningContent: "想一下"}, PerfModeThinking},
		{"有推理 token", &schema.Message{ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{
			CompletionTokensDetails: schema.CompletionTokensDetails{ReasoningTokens: 5},
		}}}, PerfModeThinking},
		{"直接回答", &schema.Message{Content: "你好"}, PerfModeDirect},
		{"空消息", nil, PerfModeDirect},
	}
	for _, c := range cases {
		if got := llmMode(c.msg); got != c.want {
			t.Errorf("%s: llmMode = %q, want %q", c.name, got, c.want)
		}
	}
}

// 摘要从事件数组汇总，包含深度思考次数
func TestPerfRecorderSummary(t *testing.T) {
	configkit.Set(configkey.LLMModel, "m")
	r := NewPerfRecorder(nil, "s5")
	r.data = PerfData{
		{Type: PerfTypeLLM, Mode: PerfModeThinking, DurationMs: 1000, TotalTokens: 100},
		{Type: PerfTypeTool, DurationMs: 200},
		{Type: PerfTypeLLM, Mode: PerfModeDirect, DurationMs: 2000, TotalTokens: 200},
	}
	s := r.Summary()
	for _, want := range []string{"LLM 2 次 / 3s", "工具 1 次 / 200ms", "深度思考 1 次", "tokens 300"} {
		if !strings.Contains(s, want) {
			t.Errorf("摘要 %q 缺少 %q", s, want)
		}
	}
}
