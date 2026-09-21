package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/service/configkit"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// bindCLI 复刻 cli 的真实调用链：bindDefaultFlags(root) -> AddChildCMD ->
// decoCmd 中「先 ParseFlags 再 bind(child) 再 loadConfig」的顺序。
// cfg 传 "" 表示不加载配置文件（纯 flag 启动）。
func bindCLI(t *testing.T, cfg string, args ...string) {
	t.Helper()
	viper.Reset()
	root := &cobra.Command{Use: "main"}
	bindDefaultFlags(root)
	child := &cobra.Command{Use: "agent", Run: func(c *cobra.Command, args []string) {}}
	root.AddCommand(child)
	if err := child.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	bind(child)
	if cfg != "" {
		viper.Set("config", cfg)
		loadConfig()
	}
}

// 无配置文件、无命令行传参时，agent/ragflow 各键取到的值必须与改动前一致：
//   - 带兜底参数的 configkit.Get*(key, def) 命中业务侧 def（viper.IsSet 对未改动的 flag 为 false）；
//   - 不带兜底参数的 configkit.GetString(key) 命中 viper 的 flag 默认值，
//     故 bind.go 里配置的默认值必须与业务侧默认值逐项相等（本用例即校验该约束）。
func TestBindAgentRagflowFallsBackToCodeDefault(t *testing.T) {
	bindCLI(t, "")

	if got := configkit.GetInt(configkey.AgentMaxIterations, 20); got != 20 {
		t.Errorf("agent.maxIterations = %d, want 20", got)
	}
	if got := configkit.GetInt(configkey.RagflowPageSize, 5); got != 5 {
		t.Errorf("ragflow.pageSize = %d, want 5", got)
	}
	if got := configkit.GetInt(configkey.RagflowKnnTopK, 1024); got != 1024 {
		t.Errorf("ragflow.knnTopK = %d, want 1024", got)
	}
	if got := configkit.GetInt(configkey.RagflowTimeoutSeconds, 30); got != 30 {
		t.Errorf("ragflow.timeoutSeconds = %d, want 30", got)
	}
	if got := configkit.GetInt(configkey.LLMMaxTokens); got != 0 {
		t.Errorf("llm.maxTokens = %d, want 0", got)
	}
	if got := configkit.GetBool(configkey.AgentStream, true); got != true {
		t.Errorf("agent.stream = %v, want true", got)
	}
	if got := configkit.GetBool(configkey.RagflowKeyword, false); got != false {
		t.Errorf("ragflow.keyword = %v, want false", got)
	}
	// GetString(key) 不带兜底参数时会命中 viper 的 flag 默认值，
	// 故此处断言的正是 bind.go 里配置的默认值必须与业务侧默认值一致
	if got := configkit.GetString(configkey.LLMApiType); got != "openai-chat-completions" {
		t.Errorf("llm.apiType = %q, want openai-chat-completions", got)
	}
	if got := configkit.GetString(configkey.RagflowSimilarityThreshold); got != "0.2" {
		t.Errorf("ragflow.similarityThreshold = %q, want 0.2", got)
	}
	if got := configkit.GetString(configkey.RagflowVectorSimilarityWeight); got != "0.3" {
		t.Errorf("ragflow.vectorSimilarityWeight = %q, want 0.3", got)
	}
	for _, k := range []string{
		configkey.LLMBaseUrl, configkey.LLMApiKey, configkey.LLMModel,
		configkey.AgentSkillsDir, configkey.AgentCheckpointDir,
		configkey.AgentWorkspaceDir, configkey.AgentSessionDir,
		configkey.RagflowBaseUrl, configkey.RagflowApiKey, configkey.RagflowDatasetIds,
	} {
		if got := configkit.GetString(k); got != "" {
			t.Errorf("%s = %q, want empty", k, got)
		}
	}
}

// config.yml 的值必须生效（新增 flag 绑定不得抢占配置文件）
func TestBindAgentRagflowConfigFileWins(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yml")
	content := `
agent:
  stream: false
  maxIterations: 3
ragflow:
  baseUrl: "http://x:9380"
  datasetIds: "d1,d2"
  pageSize: 9
  similarityThreshold: 0.9
  vectorSimilarityWeight: 0.7
  knnTopK: 7
  keyword: true
  timeoutSeconds: 5
`
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	bindCLI(t, cfg)

	if got := configkit.GetBool(configkey.AgentStream, true); got != false {
		t.Errorf("agent.stream = %v, want false", got)
	}
	if got := configkit.GetInt(configkey.AgentMaxIterations, 999); got != 3 {
		t.Errorf("agent.maxIterations = %d, want 3", got)
	}
	if got := configkit.GetInt(configkey.RagflowPageSize, 999); got != 9 {
		t.Errorf("ragflow.pageSize = %d, want 9", got)
	}
	if got := configkit.GetInt(configkey.RagflowKnnTopK, 999); got != 7 {
		t.Errorf("ragflow.knnTopK = %d, want 7", got)
	}
	if got := configkit.GetBool(configkey.RagflowKeyword, false); got != true {
		t.Errorf("ragflow.keyword = %v, want true", got)
	}
	if got := configkit.GetInt(configkey.RagflowTimeoutSeconds, 999); got != 5 {
		t.Errorf("ragflow.timeoutSeconds = %d, want 5", got)
	}
	if got := configkit.GetString(configkey.RagflowBaseUrl, "WRONG"); got != "http://x:9380" {
		t.Errorf("ragflow.baseUrl = %q, want http://x:9380", got)
	}
	if got := configkit.GetString(configkey.RagflowDatasetIds, "WRONG"); got != "d1,d2" {
		t.Errorf("ragflow.datasetIds = %q, want d1,d2", got)
	}
	// YAML 浮点经 viper.GetString 必须能原样读出（ragflow 的 getFloatConfig 依赖此路径）
	for _, tc := range []struct {
		key  string
		want string
	}{
		{configkey.RagflowSimilarityThreshold, "0.9"},
		{configkey.RagflowVectorSimilarityWeight, "0.7"},
	} {
		raw := configkit.GetString(tc.key)
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || strconv.FormatFloat(v, 'g', -1, 64) != tc.want {
			t.Errorf("%s raw=%q parsed=%v err=%v, want %s", tc.key, raw, v, err, tc.want)
		}
	}
}

// CLI 显式传参必须覆盖 config.yml（bind 让 agent/ragflow 键获得命令行覆盖能力）
func TestBindAgentRagflowCLIOverridesConfig(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(cfg, []byte("agent:\n  maxIterations: 3\n  stream: false\nragflow:\n  baseUrl: \"http://cfg:1\"\n  pageSize: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bindCLI(t, cfg,
		"--"+configkey.AgentMaxIterations+"=42",
		"--"+configkey.AgentStream+"=true",
		"--"+configkey.RagflowBaseUrl+"=http://cli:2",
		"--"+configkey.RagflowPageSize+"=8",
		"--"+configkey.RagflowSimilarityThreshold+"=0.5",
	)

	if got := configkit.GetInt(configkey.AgentMaxIterations, 999); got != 42 {
		t.Errorf("agent.maxIterations = %d, want 42", got)
	}
	if got := configkit.GetBool(configkey.AgentStream, false); got != true {
		t.Errorf("agent.stream = %v, want true", got)
	}
	if got := configkit.GetString(configkey.RagflowBaseUrl, "WRONG"); got != "http://cli:2" {
		t.Errorf("ragflow.baseUrl = %q, want http://cli:2", got)
	}
	if got := configkit.GetInt(configkey.RagflowPageSize, 999); got != 8 {
		t.Errorf("ragflow.pageSize = %d, want 8", got)
	}
	raw := configkit.GetString(configkey.RagflowSimilarityThreshold)
	if v, err := strconv.ParseFloat(raw, 64); err != nil || v != 0.5 {
		t.Errorf("ragflow.similarityThreshold raw=%q parsed=%v err=%v, want 0.5", raw, v, err)
	}
}
