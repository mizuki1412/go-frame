package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cloudwego/eino-ext/adk/backend/local"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/agentsmd"
	fsmw "github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/example/go-frame/agent/runtime/sessionstore"
	"github.com/example/go-frame/agent/tools"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/service/configkit"
	"github.com/example/go-frame/pkg/service/logkit"
)

// agentInstruction agent 系统指令：明确告知其拥有文件系统/技能工具，
// 鼓励通过工具调用进行多步推理（ReAct 自循环），而非直接给一段文本回答。
const agentInstruction = `You are a helpful assistant that can use tools to accomplish tasks.

You have access to filesystem tools (ls, read_file, write_file, edit_file, glob, grep, execute)
and a skill tool. Use them to explore the local project, read files, and gather facts before
answering. When a task needs more than one step (e.g. "read X, then summarize the tech stack"),
keep calling tools iteratively until you have everything needed, then give a final answer.

IMPORTANT: this agent runs on Windows. All file tools operate inside a workspace directory
(the program's current working directory). Paths in tool calls that are not absolute are resolved
against the workspace — prefer short relative paths like "go.mod" or "agent/runner.go".
Do NOT use slash-prefixed paths like "/go.mod" (they are treated as workspace-relative, not
drive roots). You may call ls with no path to list the workspace first.

当用户请求匹配某个 skill 时，先调用 skill 工具读取并按照 skill 说明完成任务。

当用户问题涉及知识库领域的文档或事实（内部资料、产品文档、领域知识等），且你的
既有知识与本地工作区文件都不足以回答时，先调用 rag_search 工具检索知识库，
再基于检索结果作答并注明出处；检索不到时如实说明，不要编造。

Tools whose names are prefixed with "<server>__" come from configured MCP servers;
use them when their descriptions match the task.`

// NewRunner 创建带 skill + filesystem 中间件的 Agent Runner
//
// ChatModelAgent 内置 ReAct 循环（model → tool call → model，最多 MaxIterations 轮），
// 循环能否自转取决于模型是否发起工具调用 —— 由 instruction 与中间件注入的工具共同驱动。
//
// store 非 nil 时作为 RunnerConfig.CheckPointStore 接入：运行被中断（cancel/interrupt）
// 时执行状态落盘，可用 Runner.Resume 恢复；store 为 nil 则不启用 checkpoint。
func NewRunner(cm model.ToolCallingChatModel, store adk.CheckPointStore) (*adk.Runner, error) {
	ctx := context.Background()
	skillsDir, _ := filepath.Abs(configkit.GetString(configkey.AgentSkillsDir))
	stream := configkit.GetBool(configkey.AgentStream, true)
	maxIter := configkit.GetInt(configkey.AgentMaxIterations, 20)

	// local backend 提供文件/脚本执行能力，供 filesystem 中间件与 skill 脚本使用
	be, err := local.NewBackend(ctx, &local.Config{})
	if err != nil {
		return nil, exception.New("创建 local backend 失败: " + err.Error())
	}
	// Windows 下 local backend 的 GlobInfo/GrepRaw 在目标目录不存在时会硬报错
	// （"failed to walk directory" / rg 非零退出），导致 agent 循环崩溃；
	// 且其 Execute 硬编码 /bin/sh，Windows 下无法启动。包装后：GlobInfo/GrepRaw
	// 改为返回空结果（与 LsInfo 语义一致），Execute/ExecuteStreaming 改用
	// resolveShell 解析出的 shell，其余方法原样委托。
	// 同时注入工作区目录：工具未指明绝对路径时相对工作区解析（见 resolveWorkspace）。
	// StreamingShell 同样用 safeBe：execute 工具走 wrapper 的 ExecuteStreaming，
	// 才能用上 resolveShell，而不是 local backend 硬编码的 /bin/sh
	safeBe := &windowsSafeBackend{Local: be, workspace: resolveWorkspace()}
	fsm, err := fsmw.New(ctx, &fsmw.MiddlewareConfig{
		Backend:        safeBe,
		StreamingShell: safeBe,
	})
	if err != nil {
		return nil, exception.New("创建 filesystem 中间件失败: " + err.Error())
	}

	// 从 skills 目录加载 skill；目录不存在时跳过，不报错
	handlers := []adk.ChatModelAgentMiddleware{fsm}
	if _, err := os.Stat(skillsDir); err == nil {
		skillBackend, err := skill.NewBackendFromFilesystem(ctx, &skill.BackendFromFilesystemConfig{
			Backend: be,
			BaseDir: skillsDir,
		})
		if err != nil {
			return nil, exception.New("创建 skill backend 失败: " + err.Error())
		}
		sm, err := skill.NewMiddleware(ctx, &skill.Config{
			Backend: skillBackend,
		})
		if err != nil {
			return nil, exception.New("创建 skill 中间件失败: " + err.Error())
		}
		handlers = append(handlers, sm)
	}

	// 上下文管理（均为模型调用前的临时改写，不落会话历史，幂等可重放）：
	// 1) reduction——单条工具结果超长时卸载到本地文件并原位替换为截断提示；
	//    历史中工具结果总 token 超限时把较早结果替换为占位（原文可经 read_file 取回）
	if mw, err := newReductionMiddleware(ctx, safeBe); err != nil {
		return nil, err
	} else if mw != nil {
		handlers = append(handlers, mw)
	}
	// 2) summarization——会话历史 token 超阈值时把旧历史压缩为一条摘要消息
	if mw, err := newSummarizationMiddleware(ctx, cm); err != nil {
		return nil, err
	} else if mw != nil {
		handlers = append(handlers, mw)
	}
	// 3) agentsmd——模型调用前把工作区的 AGENTS.md 临时注入输入消息（不进会话历史）
	if mw, err := newAgentsMdMiddleware(ctx, resolveWorkspace()); err != nil {
		return nil, err
	} else if mw != nil {
		handlers = append(handlers, mw)
	}

	// 工具报错软化：任何工具调用失败（文件不存在、命令报错等）时，把错误信息
	// 作为工具结果文本返回给模型自行纠正，而不是整次 agent 运行以 NodeRunError 崩溃
	handlers = append(handlers, &toolErrorSoftener{})

	// 知识库检索工具：配置了 RAGFlow（ragflow.baseUrl + ragflow.datasetIds）时注册，
	// 由模型在 ReAct 循环中自主决定何时检索
	toolList := []tool.BaseTool{}
	if ragTool, err := tools.NewRagSearchTool(); err != nil {
		return nil, err
	} else if ragTool != nil {
		toolList = append(toolList, ragTool)
	}

	// MCP 服务器工具：按 mcp.servers 配置连接（map 键=服务器名，注册名加 "<服务器名>__" 前缀）；
	// 未配置时返回空，单个 server 连接失败在 LoadMCPTools 内记录日志并跳过
	mcpTools, err := LoadMCPTools(ctx)
	if err != nil {
		return nil, err
	}
	toolList = append(toolList, mcpTools...)

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "demo_agent",
		Description: "A demo agent that can load skills",
		Instruction: agentInstruction,
		Model:       cm,
		Handlers:    handlers,
		ToolsConfig: adk.ToolsConfig{
			Tools: toolList,
		},
		// MaxIterations 限制单次运行内 model→tool 循环的轮数（0 表示默认 20）
		MaxIterations: maxIter,
	})
	if err != nil {
		return nil, exception.New("创建 agent 失败: " + err.Error())
	}

	return adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: stream,
		CheckPointStore: store,
	}), nil
}

// NewCheckpointStore 按 agent.checkpointDir 配置创建目录版 CheckPointStore；
// 配置为空时返回 (nil, nil)，表示不启用 checkpoint。
func NewCheckpointStore() (adk.CheckPointStore, error) {
	if dir := configkit.GetString(configkey.AgentCheckpointDir); dir != "" {
		return sessionstore.NewFileStore(dir)
	}
	return nil, nil
}

// 上下文管理中间件的默认值。须与 bind.go 中对应 flag 的默认值逐项一致
// （viper 对未改动的 flag 走 flag 默认值兜底，业务侧默认值仅在 flag 未注册时生效）。
const (
	// summarizationTriggerTokensDefault 会话历史 token 压缩触发阈值
	summarizationTriggerTokensDefault = 100000
	// toolResultMaxCharsDefault 单条工具结果最大字符数（与 eino reduction 默认一致）
	toolResultMaxCharsDefault = 50000
	// toolResultClearTokensDefault 历史工具结果总 token 清理阈值（与 eino reduction 默认一致）
	toolResultClearTokensDefault = 160000
	// agentsMdFilesDefault 注入模型输入的 AGENTS.md 文件列表（相对工作区解析）
	agentsMdFilesDefault = "AGENTS.md"
	// agentsMdMaxBytes 单个 AGENTS.md 注入上限（超长截断），防止过大的项目说明挤占上下文
	agentsMdMaxBytes = 64 * 1024
	// summarizationUserInstruction 摘要压缩指令
	summarizationUserInstruction = `Summarize the conversation history so the assistant can continue the task without losing context.
Preserve: the user's original request and goals, key facts and decisions made, tool calls and their important results, file paths involved, and remaining work.
Respond with plain text only. Do not call tools.`
)

// newReductionMiddleware 按 agent.toolResultMaxChars / agent.toolResultClearTokens
// 配置创建 reduction 中间件；两个阶段都关闭时返回 (nil, nil) 不挂载。
// Backend 用 windowsSafeBackend（卸载文件以绝对路径写入，Write 会原样放行），
// RootDir 固定在系统临时目录（eino 默认 /tmp 在 Windows 下不可用）。
func newReductionMiddleware(ctx context.Context, be filesystem.Backend) (adk.ChatModelAgentMiddleware, error) {
	maxChars := configkit.GetInt(configkey.AgentToolResultMaxChars, toolResultMaxCharsDefault)
	clearTokens := configkit.GetInt(configkey.AgentToolResultClearTokens, toolResultClearTokensDefault)
	if maxChars <= 0 && clearTokens <= 0 {
		return nil, nil
	}
	cfg := &reduction.Config{
		Backend: be,
		// filesystem 中间件注册的读文件工具：模型可借此取回被卸载的完整原文
		ReadFileToolName:  "read_file",
		RootDir:           filepath.Join(os.TempDir(), "agent-tool-offload"),
		MaxLengthForTrunc: maxChars,
		MaxTokensForClear: int64(clearTokens),
		SkipTruncation:    maxChars <= 0,
		SkipClear:         clearTokens <= 0,
	}
	mw, err := reduction.New(ctx, cfg)
	if err != nil {
		return nil, exception.New("创建 reduction 中间件失败: " + err.Error())
	}
	return mw, nil
}

// newSummarizationMiddleware 按 agent.summarizationTriggerTokens 配置创建
// summarization 中间件；阈值 <=0 时返回 (nil, nil) 不挂载。
// 摘要由主模型生成；token 计数用 eino 默认策略（上次响应 usage 为基线，
// 增量消息按约 4 字符/token 估算）。
func newSummarizationMiddleware(ctx context.Context, cm model.ToolCallingChatModel) (adk.ChatModelAgentMiddleware, error) {
	trigger := configkit.GetInt(configkey.AgentSummarizationTriggerTokens, summarizationTriggerTokensDefault)
	if trigger <= 0 {
		return nil, nil
	}
	mw, err := summarization.New(ctx, &summarization.Config{
		Model:           cm,
		UserInstruction: summarizationUserInstruction,
		Trigger:         &summarization.TriggerCondition{ContextTokens: trigger},
		Callback: func(_ context.Context, before, after adk.ChatModelAgentState) error {
			logkit.Info("[summarization] 压缩会话历史", "before", len(before.Messages), "after", len(after.Messages))
			return nil
		},
	})
	if err != nil {
		return nil, exception.New("创建 summarization 中间件失败: " + err.Error())
	}
	return mw, nil
}

// newAgentsMdMiddleware 按 agent.agentsMdFiles 配置创建 agentsmd 中间件，
// 在模型调用前把指定文件内容临时注入输入消息；列表为空时返回 (nil, nil) 不挂载。
// 文件缺失时经 OnLoadWarning 记录并跳过，不报错。
func newAgentsMdMiddleware(ctx context.Context, workspace string) (adk.ChatModelAgentMiddleware, error) {
	var files []string
	for _, e := range strings.Split(configkit.GetString(configkey.AgentAgentsMdFiles, agentsMdFilesDefault), ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !filepath.IsAbs(e) {
			e = filepath.Join(workspace, e)
		}
		files = append(files, e)
	}
	if len(files) == 0 {
		return nil, nil
	}
	mw, err := agentsmd.New(ctx, &agentsmd.Config{
		Backend:             agentsmdFileBackend{},
		AgentsMDFiles:       files,
		PerAgentsMDMaxBytes: agentsMdMaxBytes,
		OnLoadWarning: func(filePath string, err error) {
			logkit.Info("[agentsmd] 跳过 AGENTS.md 文件", "file", filePath, "reason", err.Error())
		},
	})
	if err != nil {
		return nil, exception.New("创建 agentsmd 中间件失败: " + err.Error())
	}
	return mw, nil
}

// agentsmdFileBackend agentsmd 中间件的文件读取后端：直接读本地文件。
// 缺失文件返回 *fs.PathError（errors.Is(err, os.ErrNotExist) 为 true），
// loader 据此静默跳过并经 OnLoadWarning 提示，而非中止加载。
type agentsmdFileBackend struct{}

func (agentsmdFileBackend) Read(_ context.Context, req *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	data, err := os.ReadFile(req.FilePath)
	if err != nil {
		return nil, err
	}
	return &filesystem.FileContent{Content: string(data)}, nil
}

// windowsSafeBackend 包装 eino-ext local backend，修复其在 Windows 上的问题：
//  1. GlobInfo/GrepRaw 在路径不存在时返回空结果（与 LsInfo 的"目录不存在→空"语义对齐）；
//  2. eino-ext 明确标注 local backend "NOT Supported: Windows"——其 Execute/ExecuteStreaming
//     硬编码 /bin/sh，Windows 下不存在。此处重写为 resolveShell 解析出的 shell。
//  3. 注入工作区：工具路径未指明绝对路径时相对 workspace 解析（eino 本身没有工作区概念）。
type windowsSafeBackend struct {
	*local.Local
	// workspace 工作区目录（绝对路径），默认为程序启动时的工作目录，
	// 后续可通过配置/运行时动态指定
	workspace string
}

// resolveWorkspace 解析工作区目录：配置 agent.workspaceDir 非空时取其绝对路径
// （相对配置按程序启动目录解析），留空默认为程序启动时的工作目录。
func resolveWorkspace() string {
	if dir := configkit.GetString(configkey.AgentWorkspaceDir); dir != "" {
		abs, err := filepath.Abs(dir)
		if err == nil {
			return abs
		}
		logkit.Error("[agent] 解析工作区目录失败，回退到当前工作目录: " + err.Error())
	}
	wd, err := os.Getwd()
	if err != nil {
		// Getwd 几乎不会失败（目录被删除等），兜底退回进程相对路径语义
		logkit.Error("[agent] 获取工作目录失败: " + err.Error())
		return "."
	}
	return wd
}

// resolvePath 把工具请求里的路径解析到工作区：绝对路径原样返回；
// 相对路径（含 "/" 开头的盘根路径，Windows 下不算绝对路径）拼接到 workspace 下；
// 空路径直接返回 workspace（如 LsInfo 不带 path、Grep 全局搜索）。
// 相对路径不允许借助 .. 逃逸出工作区；路径已存在时再做符号链接归一化复查
// （防仓库内符号链接把读取/写入引到工作区外）。绝对路径按设计放行（demo 场景
// 允许引用工作区外文件），敏感文件的显式读取由 isSensitiveFile 单独拦截。
func (b *windowsSafeBackend) resolvePath(p string) (string, error) {
	if p == "" {
		return b.workspace, nil
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	joined := filepath.Join(b.workspace, p)
	if !withinDir(b.workspace, joined) {
		return "", exception.New("路径越出工作区，已拒绝: " + p)
	}
	if real, err := filepath.EvalSymlinks(joined); err == nil {
		if ws, err := filepath.EvalSymlinks(b.workspace); err == nil && !withinDir(ws, real) {
			return "", exception.New("路径经符号链接指向工作区之外，已拒绝: " + p)
		}
	}
	return joined, nil
}

// withinDir 判断 path 是否落在 dir 内（含 dir 本身）。filepath.Rel 失败
// （如跨盘符）视为不在目录内。
func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// isSensitiveFile 判断是否敏感凭据类文件（.env、私钥、证书、各工具的凭据文件等）。
// 命中即拒绝 Read/MultiModalRead 显式读取，防提示注入诱导 agent 窃取密钥。
// 仅覆盖显式读取路径；execute 命令与 grep 仍可能触达此类文件，
// 属 demo 级防护而非完整沙箱。
func isSensitiveFile(p string) bool {
	base := strings.ToLower(filepath.Base(p))
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}
	if strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") {
		return true
	}
	for _, prefix := range []string{"id_rsa", "id_ed25519", "id_ecdsa"} {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	switch base {
	case "credentials", ".npmrc", ".netrc", ".git-credentials", "secrets.yaml", "secrets.yml":
		return true
	}
	return false
}

// 编译期断言：包装器满足 filesystem.Backend 接口
var _ filesystem.Backend = (*windowsSafeBackend)(nil)

// LsInfo 重写：无参/相对路径按工作区解析。
func (b *windowsSafeBackend) LsInfo(ctx context.Context, req *filesystem.LsInfoRequest) ([]filesystem.FileInfo, error) {
	p, err := b.resolvePath(req.Path)
	if err != nil {
		return nil, err
	}
	req.Path = p
	return b.Local.LsInfo(ctx, req)
}

// Read 重写：相对路径按工作区解析；敏感凭据类文件拒绝读取。
func (b *windowsSafeBackend) Read(ctx context.Context, req *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	p, err := b.resolvePath(req.FilePath)
	if err != nil {
		return nil, err
	}
	if isSensitiveFile(p) {
		return nil, exception.New("敏感文件，禁止读取: " + req.FilePath)
	}
	req.FilePath = p
	return b.Local.Read(ctx, req)
}

// MultiModalRead 重写：相对路径按工作区解析；敏感凭据类文件拒绝读取。
func (b *windowsSafeBackend) MultiModalRead(ctx context.Context, req *filesystem.MultiModalReadRequest) (*filesystem.MultiFileContent, error) {
	p, err := b.resolvePath(req.FilePath)
	if err != nil {
		return nil, err
	}
	if isSensitiveFile(p) {
		return nil, exception.New("敏感文件，禁止读取: " + req.FilePath)
	}
	req.FilePath = p
	return b.Local.MultiModalRead(ctx, req)
}

// GlobInfo 重写：路径按工作区解析；目标目录不存在时返回空列表而非硬错误
// （"failed to walk directory"）。
func (b *windowsSafeBackend) GlobInfo(ctx context.Context, req *filesystem.GlobInfoRequest) ([]filesystem.FileInfo, error) {
	p, err := b.resolvePath(req.Path)
	if err != nil {
		return nil, err
	}
	req.Path = p
	if req.Path != "" && !pathExists(req.Path) {
		return nil, nil
	}
	return b.Local.GlobInfo(ctx, req)
}

// GrepRaw 重写：目标目录按工作区解析，目录不存在时返回空结果而非硬错误（rg 非零退出）。
func (b *windowsSafeBackend) GrepRaw(ctx context.Context, req *filesystem.GrepRequest) ([]filesystem.GrepMatch, error) {
	p, err := b.resolvePath(req.Path)
	if err != nil {
		return nil, err
	}
	req.Path = p
	if !pathExists(req.Path) {
		return []filesystem.GrepMatch{}, nil
	}
	return b.Local.GrepRaw(ctx, req)
}

// Write 重写：相对路径按工作区解析（卸载到临时目录的绝对路径原样放行）。
func (b *windowsSafeBackend) Write(ctx context.Context, req *filesystem.WriteRequest) error {
	p, err := b.resolvePath(req.FilePath)
	if err != nil {
		return err
	}
	req.FilePath = p
	return b.Local.Write(ctx, req)
}

// Edit 重写：相对路径按工作区解析。
func (b *windowsSafeBackend) Edit(ctx context.Context, req *filesystem.EditRequest) error {
	p, err := b.resolvePath(req.FilePath)
	if err != nil {
		return err
	}
	req.FilePath = p
	return b.Local.Edit(ctx, req)
}

// pathExists 判断路径（文件/目录）是否存在
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// resolveShell 返回执行命令用的 shell 及其参数。
// eino-ext local backend 硬编码 /bin/sh，Windows 下不存在，按以下顺序解析：
// 1. 直接找 /bin/sh（git bash 安装到 PATH 时命中，如 C:\Program Files\Git\bin）；
// 2. Windows 下退到 PATH 中的 sh（Git Bash 自带的 sh.exe）；
// 3. 再退到 cmd.exe（注意：此时命令按 sh 语法写会执行失败，属于最后兜底）。
func resolveShell() (string, []string) {
	if _, err := os.Stat("/bin/sh"); err == nil {
		return "/bin/sh", []string{"-c"}
	}
	if runtime.GOOS == "windows" {
		if sh, err := exec.LookPath("sh"); err == nil {
			return sh, []string{"-c"}
		}
		return "C:\\Windows\\System32\\cmd.exe", []string{"/c"}
	}
	return "/bin/sh", []string{"-c"}
}

// Execute 重写：用 resolveShell 解析出的 shell 执行命令（替代 local 硬编码的 /bin/sh）。
func (b *windowsSafeBackend) Execute(ctx context.Context, input *filesystem.ExecuteRequest) (*filesystem.ExecuteResponse, error) {
	if input.Command == "" {
		return nil, fmt.Errorf("command is required")
	}
	shell, args := resolveShell()
	cmd := exec.CommandContext(ctx, shell, append(args, input.Command)...)
	// 命令在工作区目录下执行，未指明绝对路径的文件操作落在工作区内
	cmd.Dir = b.workspace

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	exitCode := 0
	if err := cmd.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
			stdoutStr, stderrStr := stdoutBuf.String(), stderrBuf.String()
			parts := []string{fmt.Sprintf("command exited with non-zero code %d", exitCode)}
			if stdoutStr != "" {
				parts = append(parts, "[stdout]:\n"+stdoutStr)
			}
			if stderrStr != "" {
				parts = append(parts, "[stderr]:\n"+stderrStr)
			}
			return &filesystem.ExecuteResponse{
				Output:   strings.Join(parts, "\n"),
				ExitCode: &exitCode,
			}, nil
		}
		return nil, fmt.Errorf("failed to execute command: %w", err)
	}

	return &filesystem.ExecuteResponse{
		Output:   stdoutBuf.String(),
		ExitCode: &exitCode,
	}, nil
}

// ExecuteStreaming 重写：委托 Execute 并把结果一次性送入流（ponytail: 不做
// 逐块流式输出，demo 场景足够；若需实时输出再扩展为真正的 pipe 流）。
func (b *windowsSafeBackend) ExecuteStreaming(ctx context.Context, input *filesystem.ExecuteRequest) (*schema.StreamReader[*filesystem.ExecuteResponse], error) {
	res, err := b.Execute(ctx, input)
	if err != nil {
		return nil, err
	}
	sr, w := schema.Pipe[*filesystem.ExecuteResponse](1)
	if w.Send(res, nil) {
		// 流对端已关闭，结果无法送达
		return nil, fmt.Errorf("execute result stream closed")
	}
	w.Close()
	return sr, nil
}

// toolErrorSoftener 把"工具调用报错"软化为"工具结果文本"：底层 endpoint 返回错误时，
// 返回 "error: ..." 文本作为正常工具结果，模型看到后可自行纠正（换路径、改命令等），
// 而不是整次 agent 运行以 NodeRunError 崩溃。四个 Wrap* 钩子对应工具的类型分发
// （InferTool 走 Invokable/Streamable，InferEnhancedTool 走 Enhanced*，
// execute 走 Streamable）。
// ponytail: 只软化 endpoint 返回的带外错误；带内错误块（流中途发 error chunk）
// 未处理，当前 filesystem/skill 工具均不带内发错，若引入会带内发错的工具需扩展。
type toolErrorSoftener struct {
	*adk.BaseChatModelAgentMiddleware
}

func (m *toolErrorSoftener) WrapStreamableToolCall(_ context.Context, endpoint adk.StreamableToolCallEndpoint, _ *adk.ToolContext) (adk.StreamableToolCallEndpoint, error) {
	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (*schema.StreamReader[string], error) {
		sr, err := endpoint(ctx, argumentsInJSON, opts...)
		if err == nil {
			return sr, nil
		}
		sr2, w := schema.Pipe[string](1)
		if w.Send("error: "+err.Error(), nil) {
			// 对端已关闭，结果无法送达
			w.Close()
			return nil, fmt.Errorf("tool result stream closed")
		}
		w.Close()
		return sr2, nil
	}, nil
}

func (m *toolErrorSoftener) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, _ *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		out, err := endpoint(ctx, argumentsInJSON, opts...)
		if err != nil {
			return "error: " + err.Error(), nil
		}
		return out, nil
	}, nil
}

func (m *toolErrorSoftener) WrapEnhancedInvokableToolCall(_ context.Context, endpoint adk.EnhancedInvokableToolCallEndpoint, _ *adk.ToolContext) (adk.EnhancedInvokableToolCallEndpoint, error) {
	return func(ctx context.Context, toolArgument *schema.ToolArgument, opts ...tool.Option) (*schema.ToolResult, error) {
		res, err := endpoint(ctx, toolArgument, opts...)
		if err != nil {
			return errorToolResult(err), nil
		}
		return res, nil
	}, nil
}

func (m *toolErrorSoftener) WrapEnhancedStreamableToolCall(_ context.Context, endpoint adk.EnhancedStreamableToolCallEndpoint, _ *adk.ToolContext) (adk.EnhancedStreamableToolCallEndpoint, error) {
	return func(ctx context.Context, toolArgument *schema.ToolArgument, opts ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
		sr, err := endpoint(ctx, toolArgument, opts...)
		if err == nil {
			return sr, nil
		}
		sr2, w := schema.Pipe[*schema.ToolResult](1)
		if w.Send(errorToolResult(err), nil) {
			// 对端已关闭，结果无法送达
			w.Close()
			return nil, fmt.Errorf("tool result stream closed")
		}
		w.Close()
		return sr2, nil
	}, nil
}

// errorToolResult 构造"错误文本"形式的工具结果，供增强版工具钩子软化错误用
func errorToolResult(err error) *schema.ToolResult {
	return &schema.ToolResult{
		Parts: []schema.ToolOutputPart{
			{Type: schema.ToolPartTypeText, Text: "error: " + err.Error()},
		},
	}
}
