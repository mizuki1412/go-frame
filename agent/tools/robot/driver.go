//go:build windows

package robot

import (
	"context"
	"fmt"
	"time"

	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/service/logkit"
	"github.com/tailscale/win"
)

// WindowInfo 已解析的目标窗口（WindowId 即窗口句柄，矩形为屏幕物理像素坐标）
type WindowInfo struct {
	WindowId   int    // 窗口句柄
	Pid        int    // 所属进程 ID
	Process    string // 进程 exe 文件名（取不到时为空）
	Title      string
	X, Y, W, H int  // 窗口矩形（屏幕物理像素坐标）
	Foreground bool // 是否当前前台窗口
}

// toExportInfo 内部快照转导出结构
func toExportInfo(info windowInfo) WindowInfo {
	return WindowInfo{
		WindowId:   info.WindowId,
		Pid:        info.Pid,
		Process:    info.Process,
		Title:      info.Title,
		X:          info.X,
		Y:          info.Y,
		W:          info.W,
		H:          info.H,
		Foreground: info.Foreground,
	}
}

// FindWindow 按标题/进程名子串（大小写不敏感）解析目标窗口（首个可见命中）
func FindWindow(filter string) (WindowInfo, error) {
	ensureDPIAware()
	hwnd, err := findWindowHwnd(0, filter)
	if err != nil {
		return WindowInfo{}, err
	}
	return toExportInfo(windowSnapshot(hwnd)), nil
}

// WindowRect 刷新窗口当前矩形与前台状态（窗口可能被移动/最小化/失焦）
func WindowRect(windowId int) (WindowInfo, error) {
	ensureDPIAware()
	hwnd, err := findWindowHwnd(windowId, "")
	if err != nil {
		return WindowInfo{}, err
	}
	return toExportInfo(windowSnapshot(hwnd)), nil
}

// ActivateWindow 激活窗口到前台（最小化先还原）。Windows 前台锁可能拒绝
// 后台进程抢焦点，此时依次尝试经典规避手段：ALT 键技巧（系统允许最近接收
// 输入的进程取得前台）、挂接前台线程输入队列；仍失败返回错误（不做无限
// 重试，重试策略由调用方决定）
func ActivateWindow(windowId int) error {
	ensureDPIAware()
	hwnd, err := findWindowHwnd(windowId, "")
	if err != nil {
		return err
	}
	if win.IsIconic(hwnd) {
		win.ShowWindow(hwnd, win.SW_RESTORE)
	}
	activateOnce := func() bool {
		win.SetForegroundWindow(hwnd)
		time.Sleep(200 * time.Millisecond) // 等待窗口切换生效（同 desk_window_activate）
		return win.GetForegroundWindow() == hwnd
	}
	if activateOnce() {
		return nil
	}
	// 前台锁规避一：轻点 ALT 使本进程获得前台许可（对当前前台窗口是一次
	// 无害的键事件，仅在被拒路径触发）
	logkit.Info("[ActivateWindow] 激活被拒，尝试 ALT 键技巧", "windowId", windowId)
	tapAlt()
	if activateOnce() {
		return nil
	}
	// 前台锁规避二：挂接到前台线程的输入队列后再次请求
	logkit.Info("[ActivateWindow] 尝试挂接前台线程输入队列", "windowId", windowId)
	var pid uint32
	ftid := int32(win.GetWindowThreadProcessId(win.GetForegroundWindow(), &pid))
	ctid := int32(win.GetCurrentThreadId())
	if ftid != 0 && ftid != ctid && win.AttachThreadInput(ctid, ftid, true) {
		defer win.AttachThreadInput(ctid, ftid, false)
	}
	if activateOnce() {
		return nil
	}
	return exception.New(fmt.Sprintf("窗口 %d（%s）激活被系统拒绝，前台仍为其他窗口", windowId, windowTitle(hwnd)))
}

// tapAlt 轻点一次 ALT 键（按下即松开）：Windows 只允许最近接收输入的进程
// 调用 SetForegroundWindow，该技巧使本进程获得前台许可
func tapAlt() {
	const vkMenu = 0x12
	const keyeventfKeyup = 0x2
	_, _, _ = procKeybdEvent.Call(vkMenu, 0, 0, 0)
	_, _, _ = procKeybdEvent.Call(vkMenu, 0, keyeventfKeyup, 0)
}

// MouseClick 在屏幕像素坐标单击（button: left/right/middle，空默认 left）
func MouseClick(x, y int, button string, double bool) error {
	_, err := mouseClick(context.Background(), mouseClickInput{X: x, Y: y, Button: button, Double: double})
	return err
}

// MouseDrag 按住按键从起点平滑拖到终点再松开（button 空默认 left），
// Move→MouseDown→MoveSmooth→MouseUp 序列见 tools_input.go
func MouseDrag(fromX, fromY, toX, toY int, button string) error {
	_, err := mouseDrag(context.Background(), mouseDragInput{
		FromX: fromX, FromY: fromY, ToX: toX, ToY: toY, Button: button})
	return err
}

// KeyTap 单击按键，可带修饰键（ctrl/alt/shift/win）
func KeyTap(key string, modifiers []string) error {
	_, err := keyTap(context.Background(), keyTapInput{Key: key, Modifiers: modifiers})
	return err
}
