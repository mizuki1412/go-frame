package runtime

import (
	"context"

	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/service/configkit"
)

// 接口协议类型
const (
	// apiTypeOpenAIChatCompletions OpenAI chat completions 协议（/chat/completions）
	apiTypeOpenAIChatCompletions = "openai-chat-completions"
	// apiTypeAnthropicMessages Anthropic messages 协议（/v1/messages）
	apiTypeAnthropicMessages = "anthropic-messages"
)

// claudeMaxTokensDefault claude 组件 MaxTokens 为必填项，未配置时的默认上限
const claudeMaxTokensDefault = 8192

// NewChatModel 基于配置创建 ChatModel，按 llm.apiType 分派到对应协议的组件
func NewChatModel() (model.ToolCallingChatModel, error) {
	apiType := configkit.GetString(configkey.LLMApiType, apiTypeOpenAIChatCompletions)
	switch apiType {
	case apiTypeOpenAIChatCompletions:
		return newOpenAIChatModel()
	case apiTypeAnthropicMessages:
		return newClaudeChatModel()
	default:
		return nil, exception.New("不支持的 llm.apiType: " + apiType + "（支持: " + apiTypeOpenAIChatCompletions + ", " + apiTypeAnthropicMessages + "）")
	}
}

// newOpenAIChatModel 创建 OpenAI 兼容的 ChatModel（chat completions 协议）
func newOpenAIChatModel() (model.ToolCallingChatModel, error) {
	var maxTokens *int
	if mt := configkit.GetInt(configkey.LLMMaxTokens); mt > 0 {
		maxTokens = &mt
	}

	cm, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL:             configkit.GetString(configkey.LLMBaseUrl),
		APIKey:              configkit.GetString(configkey.LLMApiKey),
		Model:               configkit.GetString(configkey.LLMModel),
		MaxCompletionTokens: maxTokens,
	})
	if err != nil {
		return nil, exception.New("创建 ChatModel 失败: " + err.Error())
	}
	return cm, nil
}

// newClaudeChatModel 创建 Anthropic messages 协议的 ChatModel
func newClaudeChatModel() (model.ToolCallingChatModel, error) {
	baseURL := configkit.GetString(configkey.LLMBaseUrl)
	// claude 组件 MaxTokens 必填，未配置时回退到默认值
	maxTokens := configkit.GetInt(configkey.LLMMaxTokens)
	if maxTokens <= 0 {
		maxTokens = claudeMaxTokensDefault
	}

	cm, err := claude.NewChatModel(context.Background(), &claude.Config{
		BaseURL:   &baseURL,
		APIKey:    configkit.GetString(configkey.LLMApiKey),
		Model:     configkit.GetString(configkey.LLMModel),
		MaxTokens: maxTokens,
	})
	if err != nil {
		return nil, exception.New("创建 ChatModel 失败: " + err.Error())
	}
	return cm, nil
}
