//go:build windows

package robot

import (
	"context"
	"fmt"

	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/service/logkit"
	robotwin "github.com/go-vgo/robotgo/win"
)

// mouseMoveInput desk_mouse_move 工具入参
type mouseMoveInput struct {
	// X 目标横坐标（屏幕像素，原点在主屏左上角）
	X int `json:"x" jsonschema:"description=Target X in screen pixels."`
	// Y 目标纵坐标
	Y int `json:"y" jsonschema:"description=Target Y in screen pixels."`
}

// mouseMoveOutput desk_mouse_move 工具出参
type mouseMoveOutput struct {
	Result string `json:"result"`
}

// desk_mouse_move 移动鼠标到指定屏幕坐标（不点击）
func mouseMove(ctx context.Context, in mouseMoveInput) (mouseMoveOutput, error) {
	ensureDPIAware()
	robotwin.Move(in.X, in.Y)
	return mouseMoveOutput{Result: fmt.Sprintf("鼠标已移动到 (%d, %d)。", in.X, in.Y)}, nil
}

// mouseClickInput desk_mouse_click 工具入参
type mouseClickInput struct {
	// X 点击位置的横坐标（屏幕像素）
	X int `json:"x" jsonschema:"description=X coordinate of the click position in screen pixels."`
	// Y 点击位置的纵坐标
	Y int `json:"y" jsonschema:"description=Y coordinate of the click position in screen pixels."`
	// Button 鼠标按键：left（默认）/ right / middle
	Button string `json:"button,omitempty" jsonschema:"description=Mouse button: left (default), right or middle."`
	// Double 是否双击
	Double bool `json:"double,omitempty" jsonschema:"description=Set true to double-click."`
}

// mouseClickOutput desk_mouse_click 工具出参
type mouseClickOutput struct {
	Result string `json:"result"`
}

// desk_mouse_click 先移动鼠标到指定坐标再点击
func mouseClick(ctx context.Context, in mouseClickInput) (mouseClickOutput, error) {
	ensureDPIAware()
	button := in.Button
	if button == "" {
		button = "left"
	}
	switch button {
	case "left", "right", "middle":
	default:
		return mouseClickOutput{}, exception.New(fmt.Sprintf("不支持的按键 %q（left/right/middle）", button))
	}
	robotwin.Move(in.X, in.Y)
	if err := robotwin.Click(button, in.Double); err != nil {
		return mouseClickOutput{}, exception.New("点击失败: " + err.Error())
	}
	logkit.Info("[desk_mouse_click]", "x", in.X, "y", in.Y, "button", button, "double", in.Double)
	action := "单击"
	if in.Double {
		action = "双击"
	}
	return mouseClickOutput{Result: fmt.Sprintf("已在 (%d, %d) %s%s键。", in.X, in.Y, action, button)}, nil
}

// mouseScrollInput desk_mouse_scroll 工具入参
type mouseScrollInput struct {
	// DY 纵向滚动量：正数向上滚，负数向下滚，幅度约 3 像素/单位（建议 ±3~10）
	DY int `json:"dy" jsonschema:"description=Vertical scroll amount: positive scrolls up, negative scrolls down. Suggested magnitude 3-10."`
	// DX 横向滚动量：正数向左滚，负数向右滚
	DX int `json:"dx,omitempty" jsonschema:"description=Horizontal scroll amount: positive scrolls left, negative scrolls right."`
}

// mouseScrollOutput desk_mouse_scroll 工具出参
type mouseScrollOutput struct {
	Result string `json:"result"`
}

// desk_mouse_scroll 在当前位置滚动鼠标滚轮
func mouseScroll(ctx context.Context, in mouseScrollInput) (mouseScrollOutput, error) {
	ensureDPIAware()
	if in.DX == 0 && in.DY == 0 {
		return mouseScrollOutput{}, exception.New("dx 与 dy 不能同时为 0")
	}
	robotwin.Scroll(in.DX, in.DY)
	return mouseScrollOutput{Result: fmt.Sprintf("已滚动 dx=%d dy=%d。", in.DX, in.DY)}, nil
}

// keyTapInput desk_key_tap 工具入参
type keyTapInput struct {
	// Key 单个按键名：字母/数字（如 a、1）、F1~F12、enter、tab、esc、space、
	// backspace、delete、up/down/left/right、home、end、pageup、pagedown 等
	Key string `json:"key" jsonschema:"description=Key name: letters/digits (a, 1), F1-F12, enter, tab, esc, space, backspace, delete, up/down/left/right, home, end, pageup, pagedown, etc."`
	// Modifiers 修饰键列表，取值 ctrl / alt / shift / win
	Modifiers []string `json:"modifiers,omitempty" jsonschema:"description=Modifier keys held while tapping: ctrl, alt, shift, win. E.g. [\"ctrl\",\"shift\"] for Ctrl+Shift+key."`
}

// keyTapOutput desk_key_tap 工具出参
type keyTapOutput struct {
	Result string `json:"result"`
}

// desk_key_tap 按下并松开单个按键（可带修饰键组合，如 ctrl+s）
func keyTap(ctx context.Context, in keyTapInput) (keyTapOutput, error) {
	ensureDPIAware()
	if in.Key == "" {
		return keyTapOutput{}, exception.New("key 不能为空")
	}
	mods := make([]any, 0, len(in.Modifiers))
	for _, m := range in.Modifiers {
		mods = append(mods, m)
	}
	if err := robotwin.KeyTap(in.Key, mods...); err != nil {
		return keyTapOutput{}, exception.New("按键失败: " + err.Error())
	}
	logkit.Info("[desk_key_tap]", "key", in.Key, "modifiers", in.Modifiers)
	if len(mods) > 0 {
		return keyTapOutput{Result: fmt.Sprintf("已按 %v+%s。", in.Modifiers, in.Key)}, nil
	}
	return keyTapOutput{Result: fmt.Sprintf("已按 %s。", in.Key)}, nil
}

// keyTypeInput desk_key_type 工具入参
type keyTypeInput struct {
	// Text 要输入的文本（Unicode 事件，支持中文）
	Text string `json:"text" jsonschema:"description=Text to type. Unicode events, Chinese supported."`
	// Paste 为 true 时改走 剪贴板+Ctrl+V 粘贴（长文本/丢弃合成按键的应用更可靠），
	// 注意会覆盖当前剪贴板内容
	Paste bool `json:"paste,omitempty" jsonschema:"description=If true, paste via clipboard+Ctrl+V instead of synthesized keystrokes (more reliable for long text; overwrites the clipboard)."`
}

// keyTypeOutput desk_key_type 工具出参
type keyTypeOutput struct {
	Result string `json:"result"`
}

// desk_key_type 输入一段文本（Unicode 逐字事件；paste=true 时经剪贴板 Ctrl+V 粘贴）
func keyType(ctx context.Context, in keyTypeInput) (keyTypeOutput, error) {
	ensureDPIAware()
	if in.Text == "" {
		return keyTypeOutput{}, exception.New("text 不能为空")
	}
	if in.Paste {
		if err := clipboardWriteAll(in.Text); err != nil {
			return keyTypeOutput{}, err
		}
		if err := robotwin.KeyTap("v", "ctrl"); err != nil {
			return keyTypeOutput{}, exception.New("粘贴失败: " + err.Error())
		}
	} else {
		robotwin.TypeStr(in.Text)
	}
	logkit.Info("[desk_key_type]", "chars", len([]rune(in.Text)), "paste", in.Paste)
	return keyTypeOutput{Result: fmt.Sprintf("已输入 %d 个字符。", len([]rune(in.Text)))}, nil
}
