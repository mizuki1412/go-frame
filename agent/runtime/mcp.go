package runtime

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	einomcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/service/configkit"
	"github.com/example/go-frame/pkg/service/logkit"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// mcp.* 服务器配置字段（mcp.servers 下每个服务器的子键）
const (
	mcpFieldTransport      = "transport"
	mcpFieldCommand        = "command"
	mcpFieldArgs           = "args"
	mcpFieldEnv            = "env"
	mcpFieldURL            = "url"
	mcpFieldHeaders        = "headers"
	mcpFieldTools          = "tools"
	mcpFieldTimeoutSeconds = "timeoutSeconds"
)

// 传输协议类型
const (
	mcpTransportStdio = "stdio"
	mcpTransportSSE   = "sse"
	mcpTransportHTTP  = "http"
)

const (
	// mcpCallTimeoutDefault 单次工具调用超时默认秒数
	mcpCallTimeoutDefault = 60
	// mcpConnectTimeout 连接与初始化单个 MCP server 的超时（防死服务器挂住 agent 启动）
	mcpConnectTimeout = 15 * time.Second
)

// envPlaceholderRe 与 cli 包的 ${ENV_NAME} 展开保持一致；viper 的 AllKeys 不含
// 列表内部字段，args 数组里的占位符不会被 loadConfig 展开，故加载时统一再展开一遍
var envPlaceholderRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// mcpLoadedClients LoadMCPTools 建立的全部连接，供 CloseMCPClients 退出时清理
var mcpLoadedClients []*client.Client

// LoadMCPTools 按 mcp.servers 配置连接各 MCP server 并把其工具包装为 eino 工具。
// 配置为 map（键=服务器名，viper 会转小写），工具注册名加 "<服务器名>__" 前缀
// 以避免跨服务器及与内置工具的命名冲突。未配置时返回 (nil, nil)；
// 单个服务器连接/初始化失败仅记录日志并跳过，不阻断 agent 启动。
func LoadMCPTools(ctx context.Context) ([]tool.BaseTool, error) {
	servers := configkit.GetStringMap(configkey.McpServers)
	if len(servers) == 0 {
		return nil, nil
	}

	// 按服务器名排序注册，保证工具列表顺序稳定（go map 遍历无序）
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var all []tool.BaseTool
	seen := map[string]bool{}
	for _, name := range names {
		raw, ok := servers[name].(map[string]any)
		if !ok {
			logkit.Error("[mcp] 服务器配置格式错误，已跳过（应为 map）", "server", name)
			continue
		}
		tools, cli, err := connectMCPServer(ctx, name, raw)
		if err != nil {
			logkit.Error("[mcp] 连接 MCP server 失败，已跳过", "server", name, "error", err.Error())
			continue
		}
		mcpLoadedClients = append(mcpLoadedClients, cli)

		for _, t := range tools {
			invokable, ok := t.(tool.InvokableTool)
			if !ok {
				logkit.Error("[mcp] 工具不是可调用类型，已跳过", "server", name)
				continue
			}
			regName := sanitizeToolName(name + "__" + mcpBaseToolName(t))
			if seen[regName] {
				logkit.Error("[mcp] 工具注册名冲突，已跳过", "server", name, "tool", regName)
				continue
			}
			seen[regName] = true
			all = append(all, &prefixedMCPTool{
				InvokableTool: invokable,
				name:          regName,
				timeout:       time.Duration(mInt(raw, mcpFieldTimeoutSeconds, mcpCallTimeoutDefault)) * time.Second,
			})
		}
		logkit.Info("[mcp] 已注册 MCP server 工具", "server", name, "tools", len(tools))
	}
	return all, nil
}

// CloseMCPClients 关闭 LoadMCPTools 建立的全部 MCP 连接（含 stdio 子进程）。
// best-effort：逐个关闭并记录错误，不因单个失败中断。
func CloseMCPClients() {
	for _, c := range mcpLoadedClients {
		if err := c.Close(); err != nil {
			logkit.Error("[mcp] 关闭 MCP 连接失败: " + err.Error())
		}
	}
	mcpLoadedClients = nil
}

// connectMCPServer 按传输协议创建并初始化单个 MCP server 连接，返回其工具列表。
// stdio 由 NewStdioMCPClient 自动启动连接；sse/http 需手动 Start。
func connectMCPServer(ctx context.Context, name string, cfg map[string]any) ([]tool.BaseTool, *client.Client, error) {
	transport := mStr(cfg, mcpFieldTransport)
	if transport == "" {
		transport = mcpTransportStdio
	}

	var cli *client.Client
	switch transport {
	case mcpTransportStdio:
		command := mStr(cfg, mcpFieldCommand)
		if command == "" {
			return nil, nil, exception.New("stdio 传输需配置 command")
		}
		var err error
		cli, err = client.NewStdioMCPClient(command, envList(mMapSS(cfg, mcpFieldEnv)), mList(cfg, mcpFieldArgs)...)
		if err != nil {
			return nil, nil, exception.New("创建 stdio 客户端失败: " + err.Error())
		}
	case mcpTransportSSE, mcpTransportHTTP:
		url := mStr(cfg, mcpFieldURL)
		if url == "" {
			return nil, nil, exception.New(transport + " 传输需配置 url")
		}
		var err error
		if transport == mcpTransportSSE {
			cli, err = client.NewSSEMCPClient(url)
		} else {
			cli, err = client.NewStreamableHttpClient(url)
		}
		if err != nil {
			return nil, nil, exception.New("创建 " + transport + " 客户端失败: " + err.Error())
		}
		if err := cli.Start(ctx); err != nil {
			return nil, nil, exception.New("启动 " + transport + " 连接失败: " + err.Error())
		}
	default:
		return nil, nil, exception.New("不支持的 transport: " + transport + "（支持 stdio / sse / http）")
	}

	// 连接失败时关闭半开连接，避免泄漏 stdio 子进程
	initCtx, cancel := context.WithTimeout(ctx, mcpConnectTimeout)
	defer cancel()
	if _, err := cli.Initialize(initCtx, mcpInitializeRequest()); err != nil {
		_ = cli.Close()
		return nil, nil, exception.New("初始化 MCP 连接失败: " + err.Error())
	}

	tools, err := einomcp.GetTools(ctx, &einomcp.Config{
		Cli:          cli,
		ToolNameList: mList(cfg, mcpFieldTools),
	})
	if err != nil {
		_ = cli.Close()
		return nil, nil, exception.New("获取 MCP 工具列表失败: " + err.Error())
	}
	return tools, cli, nil
}

// mcpInitializeRequest 构造 MCP initialize 握手请求
func mcpInitializeRequest() mcp.InitializeRequest {
	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "agent-starter", Version: "1.0.0"}
	req.Params.Capabilities = mcp.ClientCapabilities{}
	return req
}

// prefixedMCPTool 给 MCP 工具叠加两件事：
//  1. 改名：Info 返回带 "<服务器名>__" 前缀的 ToolInfo（适配器 InvokableRun 内部
//     仍用原始名调用 server，故只能装饰不能直接改 Info）；
//  2. 超时：单次工具调用附加 timeout（<=0 不限制；整轮超时由 Session 层 ctx 兜底）。
type prefixedMCPTool struct {
	tool.InvokableTool
	name    string
	timeout time.Duration
}

// Info 重写：返回改名后的工具信息（描述与参数 schema 原样保留）
func (t *prefixedMCPTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info, err := t.InvokableTool.Info(ctx)
	if err != nil {
		return nil, err
	}
	return &schema.ToolInfo{Name: t.name, Desc: info.Desc, ParamsOneOf: info.ParamsOneOf}, nil
}

// InvokableRun 重写：附加超时后委托原实现
func (t *prefixedMCPTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	if t.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}
	return t.InvokableTool.InvokableRun(ctx, argumentsInJSON, opts...)
}

// mcpBaseToolName 取 MCP 工具在 server 侧的原始名（去前缀装饰）
func mcpBaseToolName(t tool.BaseTool) string {
	info, err := t.Info(context.Background())
	if err != nil {
		return ""
	}
	return info.Name
}

// sanitizeToolName 规整为 OpenAI function name 允许的字符集（字母/数字/下划线/连字符），
// 非法字符替换为下划线；超长截断到 64（重名由调用方 seen 表兜底）
func sanitizeToolName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// envList 把 map[string]string 环境变量转为 "K=V" 列表（stdio 子进程用）
func envList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// expandEnv 展开字符串中的 ${ENV_NAME} 占位符（未设置的环境变量展开为空串）
func expandEnv(s string) string {
	return envPlaceholderRe.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(m[2 : len(m)-1])
	})
}

// 以下 m* 系列从 viper 解出的 map[string]any 中按类型安全取值，
// 字符串值统一做 ${ENV} 展开

func mStr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return expandEnv(v)
	}
	return ""
}

func mInt(m map[string]any, key string, def int) int {
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return def
}

func mList(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, expandEnv(s))
		}
	}
	return out
}

func mMapSS(m map[string]any, key string) map[string]string {
	raw, ok := m[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = expandEnv(s)
		}
	}
	return out
}
