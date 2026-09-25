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

// AgentRunTimeout 单轮运行（含模型请求与工具执行的完整一轮）的整轮超时秒数，
// 0 表示不限制。默认 600（参考 Pi 系最小 Coding Agent 的 10 分钟整轮上限）。
const AgentRunTimeout = "agent.runTimeout"

// AgentSummarizationTriggerTokens 会话历史 token 触发阈值：超过后由 summarization
// 中间件把旧历史压缩为一条摘要消息（模型调用前的临时改写，不落盘）。
// 0 表示不启用摘要压缩。默认 100000。
const AgentSummarizationTriggerTokens = "agent.summarizationTriggerTokens"

// AgentToolResultMaxChars 单条工具结果的最大字符数：超出部分由 reduction 中间件
// 卸载到本地临时文件、原位替换为截断提示（模型可用 read_file 取回全文）。
// 0 表示不截断。默认 50000（与 eino reduction 默认一致）。
const AgentToolResultMaxChars = "agent.toolResultMaxChars"

// AgentToolResultClearTokens 发送模型前统计历史中工具结果总 token：超过该阈值时
// 由 reduction 中间件把较早的工具结果替换为占位提示（原文已卸载到本地文件）。
// 0 表示不清理。默认 160000（与 eino reduction 默认一致；小上下文模型建议调低）。
const AgentToolResultClearTokens = "agent.toolResultClearTokens"

// AgentAgentsMdFiles 注入模型输入的 AGENTS.md 文件列表（英文逗号分隔；
// 相对路径按 agent.workspaceDir 解析，注入发生在模型调用前、不进会话历史）。
// 留空表示不注入。默认 "AGENTS.md"（工作区无此文件时静默跳过）。
const AgentAgentsMdFiles = "agent.agentsMdFiles"
