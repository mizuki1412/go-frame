package sessionstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/service/configkit"
	"github.com/example/go-frame/pkg/service/logkit"
)

// checkpointID 会话级 checkpoint 标识：同一会话的每轮 Run 共用，
// 中断（cancel/interrupt）后执行 Runner.Resume(ctx, checkpointID) 从断点恢复。
const CheckpointID = "agent_session"

// runTimeoutDefaultSeconds 单轮运行超时默认秒数。须与 bind.go 中
// agent.runTimeout flag 的默认值一致（viper 对未改动的 flag 走 flag 默认值兜底）。
const runTimeoutDefaultSeconds = 600

// Session 维护多轮对话的会话历史并驱动 agent 自循环（ReAct）执行。
//
// ADK 的每次 Runner.Run 都是独立的一次运行，不会跨轮保留上下文；
// 因此会话历史由本结构在应用层维护：每轮把用户输入与 agent 本轮产生的
// assistant/tool 消息累积进 history，下一轮将完整历史交给 agent。
//
// store 非 nil 时每轮 Run 携带 adk.WithCheckPointID(checkpointID)：
// 运行被中断/取消时执行状态写入 CheckPointStore（落盘），Resume 可从中断点续跑。
//
// sessStore + id：每轮结束后把完整会话历史（含 user/assistant/tool 消息）
// 原子写入磁盘，进程重启后可用同一 ID 恢复对话。
//
// perf：会话性能记录器（sessStore 非 nil 时启用），在事件循环中记录每次模型调用与
// 工具调用（调用对象、开始/结束时刻、耗时），随 Save 一并落盘为调用事件数组
// （<id>/perf.json），恢复历史会话时载入继续追加。
type Session struct {
	runner    *adk.Runner
	store     adk.CheckPointStore // 为 nil 表示未启用 checkpoint
	sessStore *SessionStore       // 为 nil 表示不持久化会话历史
	perf      *PerfRecorder       // 会话性能记录（sessStore 为 nil 时为空实现）
	id        string              // 会话 ID
	history   []*schema.Message
}

// NewSession 创建一个新的会话（历史为空）。store/sessStore 传 nil 则不启用对应持久化。
func NewSession(runner *adk.Runner, store adk.CheckPointStore, sessStore *SessionStore, id string) *Session {
	return &Session{
		runner:    runner,
		store:     store,
		sessStore: sessStore,
		perf:      NewPerfRecorder(sessStore, id),
		id:        id,
	}
}

// PerfSummary 返回本会话累计性能数据的人读摘要（未启用时为 ""）
func (s *Session) PerfSummary() string {
	return s.perf.Summary()
}

// ID 返回本会话的唯一标识
func (s *Session) ID() string { return s.id }

// Save 把当前会话历史与性能数据持久化到磁盘（sessStore 为 nil 时为空操作）
func (s *Session) Save() error {
	if s.sessStore == nil {
		return nil
	}
	if err := s.sessStore.Save(s.id, s.history); err != nil {
		return err
	}
	return s.perf.Save()
}

// LoadHistory 从磁盘加载指定会话的历史与性能数据并替换当前状态（sessStore 为 nil 时报错）。
// 加载成功后当前会话 ID 切换为 sessionID，后续增量历史与性能数据写入同一会话目录。
func (s *Session) LoadHistory(ctx context.Context, sessionID string) (int, error) {
	_ = ctx
	if s.sessStore == nil {
		return 0, fmt.Errorf("未配置 session 目录，无法加载会话历史")
	}
	msgs, err := s.sessStore.Load(sessionID)
	if err != nil {
		return 0, err
	}
	if msgs == nil {
		return 0, fmt.Errorf("会话 %s 不存在或为空", sessionID)
	}
	s.history = msgs
	s.id = sessionID
	// 性能数据随会话目录一同恢复：载入已有调用事件数组，序号接续递增
	s.perf = NewPerfRecorder(s.sessStore, sessionID)
	return len(msgs), nil
}

// Reset 清空会话历史
func (s *Session) Reset() {
	s.history = nil
}

// Run 执行一轮对话并流式打印，同时维护会话历史：
// 1) 把用户输入追加进历史
// 2) 将完整历史交给 agent（ReAct 自循环，工具结果在循环内即时回填）
// 3) 把本轮 agent 产生的 assistant/tool 消息回写进历史，供下一轮继续
//
// agent.runTimeout > 0 时本轮附加整轮超时（含模型请求与工具执行）：
// 超时若走 interrupt 路径，执行状态随 checkpoint 落盘、可 Resume 续跑；
// 若以错误呈现，则回传友好的超时错误。
//
// 返回值 interrupted 表示本轮是否被中断（event.Action.Interrupted）：
// 中断时执行状态已写入 CheckPointStore，可调用 Resume 从断点继续。
func (s *Session) Run(ctx context.Context, input string) (interrupted bool, err error) {
	// 0) 按 agent.runTimeout 附加整轮超时（0 表示不限制）
	timeoutSec := configkit.GetInt(configkey.AgentRunTimeout, runTimeoutDefaultSeconds)
	if timeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()
	}

	// 1) 用户输入追加进会话历史
	s.history = append(s.history, schema.UserMessage(input))
	turnStart := len(s.history) - 1 // 本轮 user message 下标

	// 2) 以完整历史为输入驱动 agent（ReAct 循环在框架内自循环）
	// 事件循环同时记录性能数据：等待模型响应的起点（LLM 耗时）与
	// tool call 发起→工具结果返回的间隔（工具耗时）
	s.perf.LLMStart()
	events := s.runner.Run(ctx, s.history, s.runOpts()...)
	for {
		event, ok := events.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			s.perf.Interrupt() // 异常退出也结算进行中的调用，避免耗时挂到下轮
			// 本轮尚未产生任何模型输出时回滚 user message，避免历史里留下无下文的一轮
			if len(s.history) == turnStart+1 {
				s.history = s.history[:turnStart+1]
			}
			if errors.Is(event.Err, context.DeadlineExceeded) {
				return interrupted, exception.New(fmt.Sprintf("本轮运行超时（上限 %d 秒，可调大 agent.runTimeout）: %v", timeoutSec, event.Err))
			}
			return interrupted, event.Err
		}
		if event.Action != nil && event.Action.Interrupted != nil {
			s.perf.Interrupt() // 结算进行中的调用，避免耗时挂到下次 Resume
			interrupted = true
			// 整轮超时触发的取消同样走 interrupt 落盘：注明原因，resume 可从断点续跑
			if ctx.Err() != nil {
				logkit.Info("本轮运行超时被中断，checkpoint 已保存", "timeoutSeconds", timeoutSec)
			}
			break
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		msg, _, err := adk.TypedGetMessage(event)
		if err != nil {
			return interrupted, err
		}
		if msg == nil {
			continue
		}

		switch msg.Role {
		case schema.Assistant:
			s.perf.RecordAssistant(msg)
		case schema.Tool:
			s.perf.RecordToolResult(msg)
		}

		s.printMessage(msg)

		// 3) 回写会话历史：跳过 user（已手动追加）与 system（agent instruction）
		if msg.Role == schema.Assistant || msg.Role == schema.Tool {
			s.history = append(s.history, msg)
		}
	}

	// 本轮未产生任何模型输出（例如达到 MaxIterations 上限直接失败或被中断），
	// 回退到本轮 user message 之前，避免历史里留下无下文的一轮
	if !interrupted && len(s.history) == turnStart+1 {
		s.history = s.history[:turnStart+1]
	}

	// 持久化会话历史（user + 本轮已产出的 assistant/tool 消息）
	if err := s.Save(); err != nil {
		return interrupted, err
	}

	// 会话间空行分隔
	fmt.Println()
	return interrupted, nil
}

// Resume 从 CheckPointStore 断点恢复上一轮被中断的运行，
// 继续流式打印并回写会话历史。要求：store 非 nil 且上一轮发生过中断。
// 超时策略与 Run 一致（agent.runTimeout，0 表示不限制）。
func (s *Session) Resume(ctx context.Context) error {
	if s.store == nil {
		return fmt.Errorf("未启用 checkpoint（NewSession 的 store 为 nil），无法 Resume")
	}
	timeoutSec := configkit.GetInt(configkey.AgentRunTimeout, runTimeoutDefaultSeconds)
	if timeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()
	}
	// 恢复同样记录性能数据（等待模型响应的起点重置）
	s.perf.LLMStart()
	events, err := s.runner.Resume(ctx, CheckpointID)
	if err != nil {
		return err
	}
	for {
		event, ok := events.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			s.perf.Interrupt() // 异常退出也结算进行中的调用，避免耗时挂到下轮
			if errors.Is(event.Err, context.DeadlineExceeded) {
				return exception.New(fmt.Sprintf("恢复运行超时（上限 %d 秒，可调大 agent.runTimeout）: %v", timeoutSec, event.Err))
			}
			return event.Err
		}
		if event.Action != nil && event.Action.Interrupted != nil {
			// 再次中断：状态已回写 store，可再次 Resume
			s.perf.Interrupt()
			if ctx.Err() != nil {
				logkit.Info("恢复运行超时被中断，checkpoint 已保存", "timeoutSeconds", timeoutSec)
			}
			break
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		msg, _, err := adk.TypedGetMessage(event)
		if err != nil {
			return err
		}
		if msg == nil {
			continue
		}

		switch msg.Role {
		case schema.Assistant:
			s.perf.RecordAssistant(msg)
		case schema.Tool:
			s.perf.RecordToolResult(msg)
		}

		s.printMessage(msg)

		if msg.Role == schema.Assistant || msg.Role == schema.Tool {
			s.history = append(s.history, msg)
		}
	}

	// 恢复完成，持久化最终状态
	if err := s.Save(); err != nil {
		return err
	}
	fmt.Println()
	return nil
}

// runOpts 返回本轮 Run 的 AgentRunOption：启用 checkpoint 时携带
// WithCheckPointID，使 runner 在中断/取消时把执行状态写入 CheckPointStore。
func (s *Session) runOpts() []adk.AgentRunOption {
	if s.store == nil {
		return nil
	}
	return []adk.AgentRunOption{adk.WithCheckPointID(CheckpointID)}
}

// printMessage 打印单条消息（工具调用、工具结果、助手文本）
func (s *Session) printMessage(m *schema.Message) {
	if m == nil {
		return
	}
	switch m.Role {
	case schema.Tool:
		fmt.Printf("[tool %s] %s\n", m.ToolName, m.Content)
	case schema.Assistant:
		for _, tc := range m.ToolCalls {
			fmt.Printf("[tool call] %s %s\n", tc.Function.Name, tc.Function.Arguments)
		}
		if m.Content != "" {
			fmt.Print(m.Content)
		}
	}
}
