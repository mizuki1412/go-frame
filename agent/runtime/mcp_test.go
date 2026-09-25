package runtime

import (
	"context"
	"testing"

	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/service/configkit"
)

func TestSanitizeToolName(t *testing.T) {
	cases := map[string]string{
		"weather__get_forecast": "weather__get_forecast",
		"my server__tool.v2":    "my_server__tool_v2",
		"计算__搜索":                "______", // 非法字符逐个替换为下划线
	}
	for in, want := range cases {
		if got := sanitizeToolName(in); got != want {
			t.Fatalf("sanitizeToolName(%q) = %q, want %q", in, got, want)
		}
	}
	long := make([]byte, 0, 100)
	for i := 0; i < 100; i++ {
		long = append(long, 'a')
	}
	if got := sanitizeToolName(string(long)); len(got) != 64 {
		t.Fatalf("超长工具名应截断到 64: %d", len(got))
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("MCP_TEST_TOKEN", "tok-123")
	if got := expandEnv("Bearer ${MCP_TEST_TOKEN}"); got != "Bearer tok-123" {
		t.Fatalf("占位符应展开: %q", got)
	}
	if got := expandEnv("${MCP_TEST_UNDEFINED_VAR}x"); got != "x" {
		t.Fatalf("未设置的环境变量应展开为空串: %q", got)
	}
}

func TestLoadMCPToolsUnset(t *testing.T) {
	configkit.Set(configkey.McpServers, nil)
	t.Cleanup(func() { configkit.Set(configkey.McpServers, nil) })

	tools, err := LoadMCPTools(context.Background())
	if err != nil {
		t.Fatalf("未配置 mcp.servers 不应报错: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("未配置时不应产生工具: %d", len(tools))
	}
}

// TestLoadMCPToolsSkipsBadServers 验证单个 server 配置错误（未知 transport、
// 不存在的命令）只跳过该 server，不影响整体加载
func TestLoadMCPToolsSkipsBadServers(t *testing.T) {
	configkit.Set(configkey.McpServers, map[string]any{
		"badtransport": map[string]any{"transport": "grpc"},
		"badcommand":   map[string]any{"command": "definitely-not-a-real-cmd-12345", "args": []any{"x"}},
		"wrongshape":   "not-a-map",
	})
	t.Cleanup(func() { configkit.Set(configkey.McpServers, nil) })

	tools, err := LoadMCPTools(context.Background())
	if err != nil {
		t.Fatalf("坏配置应跳过而非报错: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("坏配置不应产生工具: %d", len(tools))
	}
	if len(mcpLoadedClients) != 0 {
		t.Fatalf("连接失败的 server 不应留下半开连接: %d", len(mcpLoadedClients))
	}
}

func TestEnvList(t *testing.T) {
	got := envList(map[string]string{"A": "1", "B": "2"})
	if len(got) != 2 {
		t.Fatalf("envList 长度应为 2: %v", got)
	}
	seen := map[string]bool{}
	for _, kv := range got {
		seen[kv] = true
	}
	if !seen["A=1"] || !seen["B=2"] {
		t.Fatalf("envList 内容不符: %v", got)
	}
}
