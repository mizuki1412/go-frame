//go:build windows

package robot

import (
	"context"
	"fmt"

	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/service/logkit"
	robotwin "github.com/go-vgo/robotgo/win"
)

// screenshotInput desk_screenshot 工具入参：windowId、屏幕区域、全屏三种取景
// 二选一（优先级 windowId > 区域 > 全屏）
type screenshotInput struct {
	// WindowId 目标窗口句柄（来自 desk_window_list）：截取该窗口矩形区域，0 表示不用窗口取景
	WindowId int `json:"windowId,omitempty" jsonschema:"description=Window handle from desk_window_list; capture that window's rect. 0 means use region or full screen."`
	// X 区域取景左上角横坐标（屏幕像素）
	X int `json:"x,omitempty" jsonschema:"description=Region left edge in screen pixels (used when windowId is 0)."`
	// Y 区域取景左上角纵坐标（屏幕像素）
	Y int `json:"y,omitempty" jsonschema:"description=Region top edge in screen pixels (used when windowId is 0)."`
	// Width 区域取景宽度（像素），与 Height 均为 0 表示全屏
	Width int `json:"width,omitempty" jsonschema:"description=Region width in pixels. 0 with height 0 means capture the full primary screen."`
	// Height 区域取景高度（像素）
	Height int `json:"height,omitempty" jsonschema:"description=Region height in pixels."`
	// Path 截图保存的绝对路径（.png/.jpg），留空存到系统临时目录 desk-auto/ 下
	Path string `json:"path,omitempty" jsonschema:"description=Optional absolute file path to save the PNG (e.g. C:\\Users\\me\\Desktop\\shot.png). Defaults to a timestamped file in the temp dir."`
}

// screenshotOutput desk_screenshot 工具出参
type screenshotOutput struct {
	// Path 截图文件的绝对路径
	Path string `json:"path"`
	// Width 截图宽度（像素）
	Width int `json:"width"`
	// Height 截图高度（像素）
	Height int `json:"height"`
}

// desk_screenshot 截取屏幕（全屏/区域/指定窗口矩形）并保存为图片文件，
// 返回文件路径。截取窗口前建议先 desk_window_activate。
func screenshot(ctx context.Context, in screenshotInput) (screenshotOutput, error) {
	ensureDPIAware()
	var x, y, w, h int
	switch {
	case in.WindowId > 0:
		hwnd, err := findWindowHwnd(in.WindowId, "")
		if err != nil {
			return screenshotOutput{}, err
		}
		info := windowSnapshot(hwnd)
		if info.W <= 0 || info.H <= 0 {
			return screenshotOutput{}, exception.New(fmt.Sprintf("窗口 %q 矩形无效", info.Title))
		}
		x, y, w, h = info.X, info.Y, info.W, info.H
	case in.Width > 0 && in.Height > 0:
		x, y, w, h = in.X, in.Y, in.Width, in.Height
	default:
		w, h = robotwin.GetScreenSize()
	}
	if w <= 0 || h <= 0 {
		return screenshotOutput{}, exception.New("截图宽高必须为正数")
	}

	img, err := robotwin.CaptureImg(x, y, w, h)
	if err != nil {
		return screenshotOutput{}, exception.New("屏幕取景失败: " + err.Error())
	}
	path := in.Path
	if path == "" {
		path = defaultScreenshotPath()
	}
	if err := saveScreenshotDir(path); err != nil {
		return screenshotOutput{}, err
	}
	if err := robotwin.Save(img, path); err != nil {
		return screenshotOutput{}, exception.New("保存截图失败: " + err.Error())
	}
	logkit.Info("[desk_screenshot]", "path", path, "region", fmt.Sprintf("[%d,%d %dx%d]", x, y, w, h))
	return screenshotOutput{Path: path, Width: w, Height: h}, nil
}

// screenSizeInput desk_screen_size 工具入参
type screenSizeInput struct{}

// screenSizeOutput desk_screen_size 工具出参
type screenSizeOutput struct {
	Result string `json:"result"`
}

// desk_screen_size 返回主屏分辨率与 DPI 缩放系数（鼠标/区域坐标的参照系）
func screenSize(ctx context.Context, in screenSizeInput) (screenSizeOutput, error) {
	ensureDPIAware()
	w, h := robotwin.GetScreenSize()
	return screenSizeOutput{Result: fmt.Sprintf("主屏 %dx%d（物理像素），DPI 缩放 %.2f。",
		w, h, robotwin.ScaleF())}, nil
}
