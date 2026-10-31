package runtime

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"

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

// ChatCompletionVision 一次性多模态补全：系统提示 + 用户文本 + 本地图片附件
//（base64 data URL 内联）。供截图解读等场景复用 llm.* 配置，不向调用方暴露
// eino 类型。imagePaths 为本地图片路径（png/jpg）；temperature < 0 表示不设置
//（用服务端默认）。要求所配模型支持图片输入。
func ChatCompletionVision(ctx context.Context, system, user string, imagePaths []string, temperature float32) (string, error) {
	cm, err := NewChatModel()
	if err != nil {
		return "", err
	}
	parts := []schema.ChatMessagePart{
		{Type: schema.ChatMessagePartTypeText, Text: user},
	}
	for _, p := range imagePaths {
		dataURL, err := imageDataURL(p)
		if err != nil {
			return "", err
		}
		parts = append(parts, schema.ChatMessagePart{
			Type:     schema.ChatMessagePartTypeImageURL,
			ImageURL: &schema.ChatMessageImageURL{URL: dataURL},
		})
	}
	msgs := []*schema.Message{
		{Role: schema.System, Content: system},
		{Role: schema.User, MultiContent: parts},
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

// imageDataURL 读本地图片文件并编码为 data URL（按扩展名取 MIME）
func imageDataURL(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", exception.New("读取图片失败: " + err.Error())
	}
	var mime string
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		mime = "image/png"
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	default:
		return "", exception.New("不支持的图片格式 " + filepath.Ext(path) + "（仅 png/jpg）")
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}
