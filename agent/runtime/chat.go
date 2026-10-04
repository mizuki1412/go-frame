package runtime

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/example/go-frame/pkg/class/exception"
)

// ChatCompletion 一次性文本补全（系统提示 + 单条用户消息，无工具无历史）。
// 供 agent 之外的批量任务复用 llm.* 配置（如 mod 的参数表纠偏 LLM 仲裁），
// 不向调用方暴露 eino 类型——主模块保持不直接依赖 eino。
// temperature < 0 表示不设置（用服务端默认）。
func ChatCompletion(ctx context.Context, system, user string, temperature float32) (string, error) {
	cm, err := NewChatModel()
	if err != nil {
		return "", err
	}
	msgs := []*schema.Message{
		{Role: schema.System, Content: system},
		{Role: schema.User, Content: user},
	}
	opts := []model.Option{}
	if temperature >= 0 {
		opts = append(opts, model.WithTemperature(temperature))
	}
	resp, err := cm.Generate(ctx, msgs, opts...)
	if err != nil {
		return "", exception.New("模型调用失败: " + err.Error())
	}
	return resp.Content, nil
}
