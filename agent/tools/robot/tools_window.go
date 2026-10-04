//go:build windows

package robot

import (
	"context"
	"fmt"
	"strings"

	robotwin "github.com/go-vgo/robotgo/win"
	"github.com/tailscale/win"
)

// windowListInput desk_window_list 工具入参
type windowListInput struct {
	// Filter 标题/进程名的过滤子串（大小写不敏感），留空列出全部
	Filter string `json:"filter,omitempty" jsonschema:"description=Optional case-insensitive substring to filter windows by title or process name, e.g. \"firefox\"."`
	// Limit 最多返回的窗口数（Z 序，前台窗口靠前），默认 40
	Limit int `json:"limit,omitempty" jsonschema:"description=Max number of windows to return (Z-order, foreground first). Defaults to 40."`
}

// windowListOutput desk_window_list 工具出参：模型可读的窗口清单文本
type windowListOutput struct {
	Result string `json:"result"`
}

// desk_window_list 枚举可见且有标题的顶层窗口
func windowList(ctx context.Context, in windowListInput) (windowListOutput, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 40
	}
	wins := listVisibleWindows(in.Filter, limit)
	if len(wins) == 0 {
		return windowListOutput{Result: "没有匹配的可见窗口。"}, nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "共 %d 个可见窗口（windowId 为窗口句柄，矩形为屏幕像素坐标 [x,y w×h]）：\n", len(wins))
	for _, w := range wins {
		marks := ""
		if w.Foreground {
			marks += " [前台]"
		}
		if w.Minimized {
			marks += " [最小化]"
		}
		fmt.Fprintf(&sb, "windowId=%d pid=%d process=%s [%d,%d %dx%d]%s %s\n",
			w.WindowId, w.Pid, w.Process, w.X, w.Y, w.W, w.H, marks, w.Title)
	}
	return windowListOutput{Result: sb.String()}, nil
}

// windowActivateInput desk_window_activate 工具入参：windowId 与 title 二选一，
// 同时提供时优先 windowId
type windowActivateInput struct {
	// WindowId 目标窗口句柄（来自 desk_window_list），0 表示按 title 匹配
	WindowId int `json:"windowId,omitempty" jsonschema:"description=Target window handle from desk_window_list. 0 means match by title instead."`
	// Title 目标窗口标题子串（大小写不敏感，取首个可见命中）
	Title string `json:"title,omitempty" jsonschema:"description=Case-insensitive substring of the window title, e.g. \"firefox\". Used when windowId is 0."`
}

// windowActivateOutput desk_window_activate 工具出参
type windowActivateOutput struct {
	Result string `json:"result"`
}

// desk_window_activate 激活目标窗口到前台（最小化会先还原），并返回激活后
// 的窗口信息。Windows 可能拒绝后台进程抢焦点，此时结果中会如实说明。
func windowActivate(ctx context.Context, in windowActivateInput) (windowActivateOutput, error) {
	ensureDPIAware()
	hwnd, err := findWindowHwnd(in.WindowId, in.Title)
	if err != nil {
		return windowActivateOutput{}, err
	}
	if win.IsIconic(hwnd) {
		win.ShowWindow(hwnd, win.SW_RESTORE)
	}
	win.SetForegroundWindow(hwnd)
	robotwin.MilliSleep(200)

	info := windowSnapshot(hwnd)
	if !info.Foreground {
		return windowActivateOutput{Result: fmt.Sprintf(
			"窗口 %q（windowId=%d）已请求置前，但当前前台是其他窗口，可能被系统拒绝，建议重试一次。",
			info.Title, info.WindowId)}, nil
	}
	return windowActivateOutput{Result: fmt.Sprintf("已激活窗口 %q（windowId=%d，pid=%d，[%d,%d %dx%d]）。",
		info.Title, info.WindowId, info.Pid, info.X, info.Y, info.W, info.H)}, nil
}
