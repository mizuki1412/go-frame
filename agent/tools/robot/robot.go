//go:build windows

// Package robot desk-auto 桌面操作工具集：基于 robotgo v1.1.0 的纯 Go Windows
// 实现（github.com/go-vgo/robotgo/win 子包，tailscale/win 系统调用，无 CGO），
// 为 agent 提供 截图/窗口/键鼠/剪贴板 等 desk_* 工具，供 LLM 在 ReAct
// 自循环中按用户需求调用（如"截图 firefox 当前画面"）。
// 仅导入 robotgo/win 子包而非 robotgo 根包：根包默认走 CGO 后端，
// 无 gcc 的环境编译不过，子包则 CGO_ENABLED=0 可构建。
package robot

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/example/go-frame/pkg/class/exception"
)

// registerTool 用 InferTool 创建 desk_* 工具并追加进列表，创建失败包装为
// 带工具名的异常
func registerTool[In, Out any](list []tool.BaseTool, name, desc string,
	impl func(context.Context, In) (Out, error)) ([]tool.BaseTool, error) {
	t, err := toolutils.InferTool(name, desc, impl)
	if err != nil {
		return list, exception.New("创建 " + name + " 工具失败: " + err.Error())
	}
	return append(list, t), nil
}

// NewTools 创建全部桌面操作工具（desk_*）：窗口、截图、键鼠、剪贴板、等待。
// 供 desk-auto 命令经 runtime.RunnerOptions.ExtraTools 注册进 agent
func NewTools() ([]tool.BaseTool, error) {
	ensureDPIAware()
	var list []tool.BaseTool
	var err error
	if list, err = registerTool(list, "desk_window_list",
		"List visible top-level windows with their windowId, pid, process name, title, rect and foreground flag. Call this first to find the target window for automation.",
		windowList); err != nil {
		return nil, err
	}
	if list, err = registerTool(list, "desk_window_activate",
		"Bring a window to the foreground (restores it if minimized). Pass windowId from desk_window_list, or a case-insensitive title substring. A window must be foreground to receive mouse/keyboard input.",
		windowActivate); err != nil {
		return nil, err
	}
	if list, err = registerTool(list, "desk_screenshot",
		"Capture the screen (full screen, a pixel region, or a window's rect) and save it to a PNG file. Returns the absolute file path; report it to the user.",
		screenshot); err != nil {
		return nil, err
	}
	if list, err = registerTool(list, "desk_screen_size",
		"Get the primary screen resolution in physical pixels and the DPI scale factor — the coordinate reference for mouse and region operations.",
		screenSize); err != nil {
		return nil, err
	}
	if list, err = registerTool(list, "desk_mouse_move",
		"Move the mouse cursor to screen pixel coordinates (no click).",
		mouseMove); err != nil {
		return nil, err
	}
	if list, err = registerTool(list, "desk_mouse_click",
		"Move the mouse to screen pixel coordinates and click (left/right/middle, optionally double).",
		mouseClick); err != nil {
		return nil, err
	}
	if list, err = registerTool(list, "desk_mouse_scroll",
		"Scroll the mouse wheel at the current position: dy>0 scrolls up, dy<0 scrolls down; dx>0 scrolls left, dx<0 scrolls right.",
		mouseScroll); err != nil {
		return nil, err
	}
	if list, err = registerTool(list, "desk_key_tap",
		"Tap a key, optionally holding modifiers (e.g. key=\"s\" with modifiers=[\"ctrl\"] for Ctrl+S). Key names: letters, digits, F1-F12, enter, tab, esc, space, backspace, delete, up/down/left/right, home, end, pageup, pagedown.",
		keyTap); err != nil {
		return nil, err
	}
	if list, err = registerTool(list, "desk_key_type",
		"Type a text string as Unicode keystrokes (Chinese supported). Set paste=true to paste via clipboard+Ctrl+V instead — more reliable for long text or apps that drop synthesized keystrokes (overwrites the clipboard).",
		keyType); err != nil {
		return nil, err
	}
	if list, err = registerTool(list, "desk_clipboard_read",
		"Read the current clipboard text content (truncated if very long).",
		clipboardRead); err != nil {
		return nil, err
	}
	if list, err = registerTool(list, "desk_clipboard_write",
		"Write text to the clipboard. Combine with desk_key_tap ctrl+v to paste it somewhere.",
		clipboardWrite); err != nil {
		return nil, err
	}
	if list, err = registerTool(list, "desk_wait",
		"Wait for the given milliseconds — give the UI time to react after an action (animations, page loads) before the next step.",
		wait); err != nil {
		return nil, err
	}
	return list, nil
}

// screenshotDir 截图默认保存目录（用户可通过 desk_screenshot 的 path 参数改存别处）
func screenshotDir() string {
	return filepath.Join(os.TempDir(), "desk-auto")
}

// ensureDPIAware 进程级 DPI 感知：首次工具调用时设置一次即可。
// 不设置时 GetSystemMetrics/SendInput/BitBlt 走 DPI 虚拟化坐标，
// 缩放屏（125%/150%）下截图会被裁剪、鼠标坐标与截图像素错位。
var ensureDPIAware = sync.OnceFunc(func() {
	_, _, _ = procSetProcessDPIAware.Call()
})

// maxClipboardChars 剪贴板读取返回给模型的最大字符数（超出截断）
const maxClipboardChars = 20000

// defaultScreenshotPath 生成默认截图保存路径（时间戳命名避免覆盖）
func defaultScreenshotPath() string {
	name := "screenshot-" + time.Now().Format("20060102-150405.000") + ".png"
	return filepath.Join(screenshotDir(), name)
}

// saveScreenshotDir 确保截图保存目录存在
func saveScreenshotDir(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return exception.New("创建截图目录失败: " + err.Error())
	}
	return nil
}
