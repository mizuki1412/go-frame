//go:build windows

package robot

import (
	"fmt"
	"image"
	"time"
	"unsafe"

	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/service/logkit"
	robotwin "github.com/go-vgo/robotgo/win"
	"github.com/tailscale/win"
)

// pwRenderFullContent PW_RENDERFULLCONTENT：请求窗口渲染 DWM 合成内容，
// Chromium/硬件加速类窗口（Win 8.1+）缺此标志会抓到旧帧或空帧
const pwRenderFullContent = 2

// 确定性截图的实际抓取方式（ScreenshotResult.Method）
const (
	// captureMethodBitBlt 屏幕 DC 区域取景（窗口未遮挡，保真度最高）
	captureMethodBitBlt = "bitblt"
	// captureMethodPrint PrintWindow 离屏抓取（窗口被遮挡时）
	captureMethodPrint = "printwindow"
	// captureMethodActivate PrintWindow 失败后的兜底：一次性激活后区域取景
	captureMethodActivate = "bitblt-after-activate"
)

// ScreenshotResult ScreenshotWindow 的结果
type ScreenshotResult struct {
	// Path 截图文件的绝对路径
	Path string
	// Title 截图窗口的实际标题
	Title string
	// Width 截图宽度（像素）
	Width int
	// Height 截图高度（像素）
	Height int
	// Method 实际采用的抓取方式：bitblt / printwindow / bitblt-after-activate
	Method string
	// Foreground 抓取时目标窗口是否处于前台（不影响抓取成功率，仅作保真度参考）
	Foreground bool
}

// ScreenshotWindow 确定性窗口截图：找窗 → 还原最小化 → 按遮挡情况选取抓取
// 方式 → 存盘。windowId（句柄）与 title（标题子串，大小写不敏感取首个可见
// 命中）二选一，同时提供时优先 windowId；path 为空存系统临时目录 desk-auto/。
//
// 与 desk_screenshot 工具的差别：本函数为程序化直通设计，全程无键鼠操作、
// 无重试循环，也不要求窗口处于前台——未被遮挡时直接屏幕取景（前台焦点与否
// 不影响 BitBlt），被遮挡时用 PrintWindow 离屏抓取。Method 返回实际抓取方式，
// 便于调用方判断保真度。
func ScreenshotWindow(windowId int, title, path string) (ScreenshotResult, error) {
	ensureDPIAware()
	hwnd, err := findWindowHwnd(windowId, title)
	if err != nil {
		return ScreenshotResult{}, err
	}
	info := windowSnapshot(hwnd)
	if info.Minimized {
		win.ShowWindow(hwnd, win.SW_RESTORE)
		time.Sleep(300 * time.Millisecond) // 等待还原布局生效后重取矩形
		info = windowSnapshot(hwnd)
	}
	if info.W <= 0 || info.H <= 0 {
		return ScreenshotResult{}, exception.New(fmt.Sprintf("窗口 %q 矩形无效", info.Title))
	}

	foreground := info.Foreground
	method := captureMethodBitBlt
	var img image.Image
	if foreground || windowUnoccluded(hwnd, info) {
		img, err = captureWindowRect(info)
	} else {
		// 被遮挡：PrintWindow 离屏抓取；失败或输出为纯色时兜底一次性激活
		// 后区域取景（Windows 可能拒绝抢焦点，此时截到的是矩形内最顶层窗口）
		img, err = printWindowImg(hwnd, info.W, info.H)
		if err != nil {
			logkit.Info("[ScreenshotWindow] PrintWindow 不可用，兜底激活后取景", "windowId", info.WindowId, "title", info.Title, "reason", err.Error())
			method = captureMethodActivate
			img, err = activateAndCapture(hwnd, info)
			foreground = win.GetForegroundWindow() == hwnd
		} else {
			method = captureMethodPrint
		}
	}
	if err != nil {
		return ScreenshotResult{}, err
	}

	if path == "" {
		path = defaultScreenshotPath()
	}
	if err := saveScreenshotDir(path); err != nil {
		return ScreenshotResult{}, err
	}
	if err := robotwin.Save(img, path); err != nil {
		return ScreenshotResult{}, exception.New("保存截图失败: " + err.Error())
	}
	logkit.Info("[ScreenshotWindow]", "windowId", info.WindowId, "title", info.Title,
		"pid", info.Pid, "method", method,
		"region", fmt.Sprintf("[%d,%d %dx%d]", info.X, info.Y, info.W, info.H), "path", path)
	return ScreenshotResult{
		Path:       path,
		Title:      info.Title,
		Width:      info.W,
		Height:     info.H,
		Method:     method,
		Foreground: foreground,
	}, nil
}

// windowUnoccluded 判断窗口矩形中心点处最顶层的根窗口是否为本窗口
// （BitBlt 区域取景要求目标在该点未被其他窗口覆盖；前台焦点与否不影响
// 屏幕 DC 取景，故不作判断）
func windowUnoccluded(hwnd win.HWND, info windowInfo) bool {
	if info.W <= 0 || info.H <= 0 {
		return false
	}
	hit := win.WindowFromPoint(win.POINT{
		X: int32(info.X + info.W/2),
		Y: int32(info.Y + info.H/2),
	})
	if hit == 0 {
		return false
	}
	// WindowFromPoint 可能命中子窗口，两侧都归一到根窗口再比较
	return win.GetAncestor(hit, win.GA_ROOT) == win.GetAncestor(hwnd, win.GA_ROOT)
}

// captureWindowRect 屏幕 DC 区域取景窗口矩形（窗口未被遮挡时最忠实的方式）
func captureWindowRect(info windowInfo) (image.Image, error) {
	img, err := robotwin.CaptureImg(info.X, info.Y, info.W, info.H)
	if err != nil {
		return nil, exception.New("屏幕取景失败: " + err.Error())
	}
	return img, nil
}

// printWindowImg PrintWindow 离屏抓取窗口内容（不依赖窗口未被遮挡或处于
// 前台）；输出为纯色时视为失败（部分应用不支持离屏渲染，会给出空帧）
func printWindowImg(hwnd win.HWND, w, h int) (image.Image, error) {
	hScreen := win.GetDC(0)
	if hScreen == 0 {
		return nil, exception.New("GetDC 失败")
	}
	defer win.ReleaseDC(0, hScreen)

	hDC := win.CreateCompatibleDC(hScreen)
	if hDC == 0 {
		return nil, exception.New("CreateCompatibleDC 失败")
	}
	defer win.DeleteDC(hDC)

	hBmp := win.CreateCompatibleBitmap(hScreen, int32(w), int32(h))
	if hBmp == 0 {
		return nil, exception.New("CreateCompatibleBitmap 失败")
	}
	defer win.DeleteObject(win.HGDIOBJ(hBmp))

	old := win.SelectObject(hDC, win.HGDIOBJ(hBmp))
	r1, _, _ := procPrintWindow.Call(uintptr(hwnd), uintptr(hDC), pwRenderFullContent)
	win.SelectObject(hDC, old) // GetDIBits 要求位图未选入 DC
	if r1 == 0 {
		return nil, exception.New("PrintWindow 调用失败")
	}

	bi := win.BITMAPINFO{
		BmiHeader: win.BITMAPINFOHEADER{
			BiSize:        uint32(unsafe.Sizeof(win.BITMAPINFOHEADER{})),
			BiWidth:       int32(w),
			BiHeight:      -int32(h), // 负值 = 自顶向下行序
			BiPlanes:      1,
			BiBitCount:    32,
			BiCompression: win.BI_RGB,
		},
	}
	buf := make([]byte, w*h*4)
	if win.GetDIBits(hDC, hBmp, 0, uint32(h), &buf[0], &bi, win.DIB_RGB_COLORS) == 0 {
		return nil, exception.New("GetDIBits 失败")
	}
	if uniformBytes(buf) {
		return nil, exception.New("PrintWindow 输出为纯色")
	}

	// GDI 32bpp DIB 为 B,G,R,X 字节序，转 RGBA 并强制不透明
	//（离屏帧的 alpha 语义不定，PNG 存不透明截图）
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		img.Pix[i*4+0] = buf[i*4+2]
		img.Pix[i*4+1] = buf[i*4+1]
		img.Pix[i*4+2] = buf[i*4+0]
		img.Pix[i*4+3] = 0xff
	}
	return img, nil
}

// uniformBytes 判断字节切片是否全为同一值（检测 PrintWindow 空帧）
func uniformBytes(b []byte) bool {
	for _, v := range b {
		if v != b[0] {
			return false
		}
	}
	return true
}

// activateAndCapture 兜底抓取：一次性请求窗口置前（不重试、无键鼠操作）后
// 区域取景
func activateAndCapture(hwnd win.HWND, info windowInfo) (image.Image, error) {
	if win.IsIconic(hwnd) {
		win.ShowWindow(hwnd, win.SW_RESTORE)
	}
	win.SetForegroundWindow(hwnd)
	time.Sleep(200 * time.Millisecond) // 等待窗口切换生效（同 desk_window_activate）
	return captureWindowRect(info)
}
