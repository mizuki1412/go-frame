package middleware

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/example/go-frame/pkg/service/logkit"
	"github.com/example/go-frame/pkg/service/restkit/context"
	"github.com/example/go-frame/pkg/service/restkit/router"
)

// 色板按 Windows conhost 默认 16 色调色板的对比度选取：亮背景配黑字、深背景配亮白字。
// 色相含义不变：绿=成功/快、黄=警告/较慢、红=错误/慢、蓝=3xx 重定向。
const (
	colorGreen  = "\033[102;30m" // 亮绿底 + 黑字（conhost 对比度约 15:1；原 102;97 白字仅约 1.3:1）
	colorYellow = "\033[103;30m" // 亮黄底 + 黑字（约 19:1，警示牌配色；原 43;37 橄榄底灰字发糊）
	colorRed    = "\033[41;97m"  // 深红底 + 亮白字（约 8.6:1，错误白字红底的惯例）
	colorBlue   = "\033[44;97m"  // 深蓝底 + 亮白字（约 12:1，3xx 重定向）
	colorReset  = "\033[0m"
)

// colorEnabled 仅当 stderr 为终端（控制台）时输出 ANSI 颜色：
// 重定向到文件/管道时自动退化为纯文本，避免转义码污染日志。
var colorEnabled = func() bool {
	fi, err := os.Stderr.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}()

// paint 为 s 染色；未启用颜色（非终端）时原样返回。
func paint(color, s string) string {
	if !colorEnabled {
		return s
	}
	return color + s + colorReset
}

// Log 访问日志中间件：每请求一行，console 与 file 各自同源输出一次。
// 原实现每请求最多四条输出（request/response 各打一次 + 彩色行绕过 logkit 直写 stderr），
// 且 log.console=false 时彩色行仍然打印；现收敛为：
//   - 文件通道：logkit.InfoFile 结构化一行（未配置文件日志时为空操作）
//   - 控制台通道：彩色单行，仅 log.console=true 时输出
//
// 不打印 uid：取 uid 需解析会话，公开接口会被白打一次存储。
func Log() router.Handler {
	return func(ctx *context.Context) {
		t := time.Now()
		ctx.Proxy.Next()
		latency := float64(time.Since(t).Microseconds()) / 1000
		status := ctx.Proxy.Writer.Status()
		url := ctx.Request.URL.String()
		ip := ctx.ClientIp()
		logkit.InfoFile("access", "method", ctx.Request.Method, "url", url,
			"status", status, "latency", latency, "ip", ip)
		if logkit.ConsoleEnabled() {
			msg := fmt.Sprintf("msg=access %s %s %s url=%s ip=%s",
				ctx.Request.Method,
				paint(statusColor(status), strconv.Itoa(status)),
				paint(latencyColor(latency), fmtLatency(latency)),
				url, ip)
			fmt.Fprintf(os.Stderr, "time=%s level=INFO %s\n",
				t.Format("2006/01/02-15:04:05"), msg)
		}
	}
}

func statusColor(code int) string {
	if code >= 200 && code < 300 {
		return colorGreen
	}
	if code >= 300 && code < 400 {
		return colorBlue
	}
	return colorRed
}

func latencyColor(ms float64) string {
	if ms < 1000 {
		return colorGreen
	}
	if ms < 3000 {
		return colorYellow
	}
	return colorRed
}

func fmtLatency(ms float64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%7.2fs", ms/1000)
	}
	return fmt.Sprintf("%7.2fms", ms)
}
