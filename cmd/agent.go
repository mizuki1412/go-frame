package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/example/go-frame/agent/runtime"
	"github.com/example/go-frame/agent/runtime/sessionstore"
	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/service/configkit"
	"github.com/example/go-frame/pkg/service/logkit"
	"github.com/spf13/cobra"
)

// AgentCMD 返回 agent demo 子命令：ReAct 自循环 + 多轮会话历史交互 + 会话历史持久化
func AgentCMD() *cobra.Command {
	return &cobra.Command{
		Use:   "agent",
		Short: "大模型 Agent 交互 demo（ReAct 自循环、会话历史、流式输出、会话持久化）",
		Run: func(cmd *cobra.Command, args []string) {
			if err := run(); err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR] agent demo 运行失败: %v\n", err)
				os.Exit(1)
			}
		},
	}
}

func run() error {
	cm, err := runtime.NewChatModel()
	if err != nil {
		return err
	}

	// 按 agent.checkpointDir 配置创建 CheckPointStore（为空则不启用 checkpoint）
	store, err := runtime.NewCheckpointStore()
	if err != nil {
		return err
	}
	runner, err := runtime.NewRunner(cm, store)
	if err != nil {
		return err
	}

	// 按 agent.sessionDir 配置创建会话历史存储（为空则不持久化）
	var sessStore *sessionstore.SessionStore
	if dir := configkit.GetString(configkey.AgentSessionDir); dir != "" {
		sessStore, err = sessionstore.NewSessionStore(dir)
		if err != nil {
			return err
		}
	}

	// 每次启动生成新的唯一 session ID
	id, err := sessionstore.NewSessionID()
	if err != nil {
		return err
	}

	session := sessionstore.NewSession(runner, store, sessStore, id)
	checkpointEnabled := store != nil
	sessEnabled := sessStore != nil

	hint := "每轮保留会话历史"
	if checkpointEnabled || sessEnabled {
		hint = "会话历史持久化"
		if checkpointEnabled {
			hint += " + 中断 checkpoint"
		}
	}
	//fmt.Println("agent session started", "id", id, "hint", hint)

	ready := fmt.Sprintf("session %s，exit 退出，clear 重置", id)
	if sessEnabled {
		ready += "，session <id> 恢复历史会话"
	}
	if checkpointEnabled {
		ready += "，resume 从断点继续"
	}
	fmt.Println("Agent 就绪，输入内容开始对话，" + ready + "。")

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("\n> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}

		// "session <id>" 命令：恢复指定历史会话
		if strings.HasPrefix(input, "session ") {
			targetID := strings.TrimSpace(strings.TrimPrefix(input, "session"))
			if targetID == "" {
				fmt.Println("用法：session <id>")
				continue
			}
			if sessStore == nil {
				fmt.Println("未配置 session 目录，无法恢复历史会话。")
				continue
			}
			n, err := session.LoadHistory(context.Background(), targetID)
			if err != nil {
				logkit.Error("恢复会话失败: " + err.Error())
				fmt.Fprintf(os.Stderr, "[ERROR] 恢复会话失败: %v\n", err)
				continue
			}
			logkit.Info("会话已恢复", "id", targetID, "messages", n)
			fmt.Printf("已恢复会话 %s（%d 条消息），继续输入即可。\n", targetID, n)
			if summary := session.PerfSummary(); summary != "" {
				fmt.Println("历史性能：" + summary)
			}
			continue
		}

		switch input {
		case "exit", "quit":
			if summary := session.PerfSummary(); summary != "" {
				fmt.Println("本会话性能：" + summary)
			}
			fmt.Println("再见。")
			return nil
		case "clear":
			session.Reset()
			if d, ok := store.(adk.CheckPointDeleter); ok {
				_ = d.Delete(context.Background(), sessionstore.CheckpointID) // 清理断点，避免残留
			}
			logkit.Info("agent session cleared")
			fmt.Println("会话历史已重置。")
		case "resume":
			if store == nil {
				fmt.Println("未启用 checkpoint，无法 resume。")
				continue
			}
			if err := session.Resume(context.Background()); err != nil {
				logkit.Error("agent resume 失败: " + err.Error())
				fmt.Fprintf(os.Stderr, "[ERROR] agent resume 失败: %v\n", err)
			}
		default:
			interrupted, err := session.Run(context.Background(), input)
			if err != nil {
				logkit.Error("agent 执行失败: " + err.Error())
				fmt.Fprintf(os.Stderr, "[ERROR] agent 执行失败: %v\n", err)
			} else if interrupted {
				logkit.Info("本轮被中断，checkpoint 已保存")
				fmt.Println("（本轮被中断，执行状态已保存。输入 resume 从断点继续。）")
			}
		}
	}
}
