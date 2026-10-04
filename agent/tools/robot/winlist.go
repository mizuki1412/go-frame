//go:build windows

package robot

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/example/go-frame/pkg/class/exception"
	robotwin "github.com/go-vgo/robotgo/win"
	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
)

var user32 = windows.NewLazySystemDLL("user32.dll")

var (
	procEnumWindows        = user32.NewProc("EnumWindows")
	procSetProcessDPIAware = user32.NewProc("SetProcessDPIAware")
	procIsWindow           = user32.NewProc("IsWindow")
)

// syscall.NewCallback 分配的回调槽永不释放，故包级注册一个单例回调，
// 每次枚举在锁内换入 visitor（同 robotgo win 包 enumState 的做法）
var enumState struct {
	mu      sync.Mutex
	visitor func(hwnd win.HWND) bool
}

var enumCallback = syscall.NewCallback(func(hwnd, lparam uintptr) uintptr {
	if enumState.visitor != nil && enumState.visitor(win.HWND(hwnd)) {
		return 1 // continue
	}
	return 0 // stop
})

// enumWindows 枚举顶层窗口（按 Z 序，前台窗口靠前），visitor 返回 false 停止
func enumWindows(cb func(hwnd win.HWND) bool) {
	enumState.mu.Lock()
	defer enumState.mu.Unlock()
	enumState.visitor = cb
	defer func() { enumState.visitor = nil }()
	procEnumWindows.Call(enumCallback, 0)
}

// windowTitle 返回窗口标题
func windowTitle(hwnd win.HWND) string {
	n := win.GetWindowTextLength(hwnd)
	if n <= 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	win.GetWindowText(hwnd, &buf[0], int32(len(buf)))
	return windows.UTF16ToString(buf)
}

// windowPid 返回窗口所属进程 ID
func windowPid(hwnd win.HWND) int {
	var pid uint32
	win.GetWindowThreadProcessId(hwnd, &pid)
	return int(pid)
}

// windowInfo 一个可操作的顶层窗口
type windowInfo struct {
	WindowId   int    // 窗口句柄，作为其余工具的 windowId 入参
	Pid        int    // 所属进程 ID
	Process    string // 进程 exe 文件名（取不到时为空）
	Title      string
	X, Y, W, H int  // 窗口矩形（屏幕物理像素坐标）
	Foreground bool // 是否当前前台窗口
	Minimized  bool // 是否最小化（矩形为系统占位坐标，激活时会自动还原）
}

// isWindow 判断句柄是否仍是一个有效窗口（tailscale/win 未绑定 IsWindow）
func isWindow(hwnd win.HWND) bool {
	r1, _, _ := procIsWindow.Call(uintptr(hwnd))
	return r1 != 0
}

// findWindowHwnd 按 windowId（句柄）或标题子串（大小写不敏感，首个可见命中）
// 解析目标窗口；两者都未提供时返回前台窗口
func findWindowHwnd(windowId int, title string) (win.HWND, error) {
	if windowId > 0 {
		hwnd := win.HWND(windowId)
		if !isWindow(hwnd) {
			return 0, exception.New(fmt.Sprintf("窗口句柄 %d 不存在或已关闭", windowId))
		}
		return hwnd, nil
	}
	title = strings.ToLower(strings.TrimSpace(title))
	if title == "" {
		return win.GetForegroundWindow(), nil
	}
	var found win.HWND
	enumWindows(func(hwnd win.HWND) bool {
		if win.IsWindowVisible(hwnd) &&
			strings.Contains(strings.ToLower(windowTitle(hwnd)), title) {
			found = hwnd
			return false
		}
		return true
	})
	if found == 0 {
		return 0, exception.New(fmt.Sprintf("未找到标题包含 %q 的可见窗口", title))
	}
	return found, nil
}

// windowSnapshot 读取窗口当前信息（标题/进程/矩形/前台状态）
func windowSnapshot(hwnd win.HWND) windowInfo {
	info := windowInfo{
		WindowId:   int(hwnd),
		Pid:        windowPid(hwnd),
		Title:      windowTitle(hwnd),
		Foreground: win.GetForegroundWindow() == hwnd,
		Minimized:  win.IsIconic(hwnd),
	}
	if info.Pid > 0 {
		if path, err := robotwin.FindPath(info.Pid); err == nil {
			info.Process = filepath.Base(path)
		}
	}
	var r win.RECT
	if win.GetWindowRect(hwnd, &r) {
		info.X, info.Y = int(r.Left), int(r.Top)
		info.W, info.H = int(r.Right-r.Left), int(r.Bottom-r.Top)
	}
	return info
}

// listVisibleWindows 枚举可见且有标题的顶层窗口（按 Z 序），filter 非空时
// 按标题/进程名子串过滤（大小写不敏感），limit 上限截断
func listVisibleWindows(filter string, limit int) []windowInfo {
	ensureDPIAware()
	fg := win.GetForegroundWindow()
	filter = strings.ToLower(strings.TrimSpace(filter))
	var out []windowInfo
	enumWindows(func(hwnd win.HWND) bool {
		if len(out) >= limit {
			return false
		}
		if !win.IsWindowVisible(hwnd) {
			return true
		}
		title := windowTitle(hwnd)
		if title == "" {
			return true
		}
		info := windowSnapshot(hwnd)
		if filter != "" &&
			!strings.Contains(strings.ToLower(info.Title), filter) &&
			!strings.Contains(strings.ToLower(info.Process), filter) {
			return true
		}
		info.Foreground = hwnd == fg
		out = append(out, info)
		return true
	})
	return out
}
