package configkey

// llm.* 大模型配置（OpenAI chat completions / Anthropic messages 兼容接口）

// LLMBaseUrl 大模型服务地址（OpenAI 兼容端点）
const LLMBaseUrl = "llm.baseUrl"

// LLMApiKey 大模型 API Key
const LLMApiKey = "llm.apiKey"

// LLMModel 大模型名称
const LLMModel = "llm.model"

// LLMMaxTokens 限制单次生成最大 token 数（anthropic-messages 协议下必填，未配置时默认 8192）
const LLMMaxTokens = "llm.maxTokens"

// LLMApiType 接口协议类型，支持 openai-chat-completions（默认）/ anthropic-messages
const LLMApiType = "llm.apiType"

// agent.* Agent 运行时配置

// AgentSkillsDir skill 目录（每个子目录一个 skill，含 SKILL.md）
const AgentSkillsDir = "agent.skillsDir"

// AgentStream 是否开启流式输出
const AgentStream = "agent.stream"

// AgentMaxIterations 单次运行内 model→tool 循环的最大轮数（0 表示框架默认 20）
const AgentMaxIterations = "agent.maxIterations"

// AgentCheckpointDir ADK CheckPointStore 的本地存储目录（用于中断后 Resume 恢复执行状态）。
// 留空表示不启用 checkpoint 持久化（Runner 的 CheckPointStore 为 nil）。
const AgentCheckpointDir = "agent.checkpointDir"

// AgentWorkspaceDir agent 工作区目录：文件系统工具（ls/read/write/edit/glob/grep/execute）
// 的操作路径未指明绝对路径时，相对该目录解析（命令也在该目录下执行）。
// 留空默认取程序启动时的工作目录（os.Getwd()）。
const AgentWorkspaceDir = "agent.workspaceDir"

// AgentSessionDir 会话历史的本地持久化目录（每个 session ID 一个目录：
// history.json 存完整消息历史，perf.json 存调用事件数组（每次模型/工具调用的耗时明细），
// 进程重启后输入 session <id> 可恢复历史对话）。留空表示不持久化会话历史。
const AgentSessionDir = "agent.sessionDir"
