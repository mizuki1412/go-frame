//go:build windows

package robot

import (
	"context"
	"fmt"
	"time"

	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/service/logkit"
	"github.com/go-vgo/robotgo/clipboard"
)

// clipboardWriteAll 写入剪贴板文本（供 desk_key_type paste 模式复用）
func clipboardWriteAll(text string) error {
	if err := clipboard.WriteAll(text); err != nil {
		return exception.New("写入剪贴板失败: " + err.Error())
	}
	return nil
}

// clipboardReadInput desk_clipboard_read 工具入参
type clipboardReadInput struct{}

// clipboardReadOutput desk_clipboard_read 工具出参
type clipboardReadOutput struct {
	Result string `json:"result"`
}

// desk_clipboard_read 读取当前剪贴板的文本内容（超长截断）
func clipboardRead(ctx context.Context, in clipboardReadInput) (clipboardReadOutput, error) {
	text, err := clipboard.ReadAll()
	if err != nil {
		return clipboardReadOutput{}, exception.New("读取剪贴板失败: " + err.Error())
	}
	if text == "" {
		return clipboardReadOutput{Result: "剪贴板为空或没有文本内容。"}, nil
	}
	runes := []rune(text)
	if len(runes) > maxClipboardChars {
		return clipboardReadOutput{Result: fmt.Sprintf("%s\n…（已截断，共 %d 字符）",
			string(runes[:maxClipboardChars]), len(runes))}, nil
	}
	return clipboardReadOutput{Result: text}, nil
}

// clipboardWriteInput desk_clipboard_write 工具入参
type clipboardWriteInput struct {
	// Text 写入剪贴板的文本
	Text string `json:"text" jsonschema:"description=Text to put on the clipboard."`
}

// clipboardWriteOutput desk_clipboard_write 工具出参
type clipboardWriteOutput struct {
	Result string `json:"result"`
}

// desk_clipboard_write 把文本写入剪贴板（配合 desk_key_tap ctrl+v 可粘贴）
func clipboardWrite(ctx context.Context, in clipboardWriteInput) (clipboardWriteOutput, error) {
	if err := clipboardWriteAll(in.Text); err != nil {
		return clipboardWriteOutput{}, err
	}
	logkit.Info("[desk_clipboard_write]", "chars", len([]rune(in.Text)))
	return clipboardWriteOutput{Result: "已写入剪贴板。"}, nil
}

// waitInput desk_wait 工具入参
type waitInput struct {
	// Ms 等待毫秒数（1~30000）
	Ms int `json:"ms" jsonschema:"description=Milliseconds to wait (1-30000)."`
}

// waitOutput desk_wait 工具出参
type waitOutput struct {
	Result string `json:"result"`
}

// desk_wait 等待指定毫秒数（UI 反应、动画、页面加载后给系统留时间）
func wait(ctx context.Context, in waitInput) (waitOutput, error) {
	if in.Ms <= 0 || in.Ms > maxWaitMs {
		return waitOutput{}, exception.New(fmt.Sprintf("ms 必须在 1~%d 之间", maxWaitMs))
	}
	time.Sleep(time.Duration(in.Ms) * time.Millisecond)
	return waitOutput{Result: fmt.Sprintf("已等待 %d ms。", in.Ms)}, nil
}

// maxWaitMs desk_wait 单次等待上限
const maxWaitMs = 30000
