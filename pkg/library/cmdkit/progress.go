package cmdkit

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// 转轮节拍与重绘节流。
const (
	progressTick     = 200 * time.Millisecond
	progressMinGap   = 100 * time.Millisecond
	spinnerFrames    = `|/-\`
	progressBarWidth = 20
)

// ProgressEnv 置 1 时跳过终端检测强制启用进度（stdout 非终端的阻塞场景）。
const ProgressEnv = "CMDKIT_PROGRESS"

// ProgressBar 控制台单行原地进度条（转轮 / 计数 / 进度条）；
// 进度写构造传入的流，日志应走 stderr 以免互扰；零值不可用，须经 NewProgressBar。
type ProgressBar struct {
	w        io.Writer
	enabled  bool
	interval time.Duration // 转轮节拍（测试可缩短）
	spinDone chan struct{}
	running  bool

	mu         sync.Mutex
	spinIndex  int
	lastDraw   time.Time
	cursorShow bool // 转轮期间隐藏光标
	lastLen    int  // 上一帧字符数，用于覆盖残影
	job        job
	spinWG     sync.WaitGroup
}

// job 当前阶段；label 为空表示无进行中的阶段。
type job struct {
	label     string
	begin     time.Time
	done      int64
	total     int64 // >0 渲染进度条
	showCount bool  // 无总数时显示累计条数
}

// NewProgressBar 创建进度条；w 非字符设备时全局禁用（重定向不产生控制符），
// 除非 ProgressEnv=1 强制启用。
func NewProgressBar(w io.Writer) *ProgressBar {
	r := &ProgressBar{w: w, interval: progressTick}
	if os.Getenv(ProgressEnv) != "1" {
		f, isFile := w.(*os.File)
		if !isFile {
			return r
		}
		st, err := f.Stat()
		if err != nil || st.Mode()&os.ModeCharDevice == 0 {
			return r
		}
	}
	r.enabled = true
	r.cursorShow = true
	return r
}

// Stage 开启未知总数的阶段：转轮 + 已用时 + 累计条数（由 Add 累加）。
func (r *ProgressBar) Stage(label string) {
	if !r.enabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.begin(job{label: label, begin: time.Now(), showCount: true})
}

// StageSet 开启已知总数的阶段：进度条 + done/total + 已用。
func (r *ProgressBar) StageSet(label string, total int64) {
	if !r.enabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.begin(job{label: label, begin: time.Now(), total: total})
}

// Add 累加当前阶段计数并刷新（受节流限制）；无阶段时静默忽略。并发安全。
func (r *ProgressBar) Add(n int64) {
	if !r.enabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.job.label == "" {
		return
	}
	r.job.done += n
	r.draw(true)
}

// Finish 结束当前阶段：清除进度行并打印完成行（含耗时）。
func (r *ProgressBar) Finish() {
	if !r.enabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.job.label == "" {
		return
	}
	elapsed := time.Since(r.job.begin)
	label := r.job.label
	r.job = job{}
	r.stopSpin(true)
	r.clearLine()
	_, _ = fmt.Fprintf(r.w, "[%s] 完成 %s\n", label, formatElapsed(elapsed.Truncate(100*time.Millisecond)))
}

// Close 退出兜底：停止转轮、清除进度行、恢复光标；幂等。
func (r *ProgressBar) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.job = job{}
	r.stopSpin(true)
	r.clearLine()
}

// Display 当前帧纯文本（无控制符），供测试与上层复用渲染逻辑。
func (r *ProgressBar) Display() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.render()
}

// begin 切换阶段并起转轮，持锁调用；未 Finish 直接切阶段时先清残留行。
func (r *ProgressBar) begin(j job) {
	if r.job.label != "" {
		r.job = job{}
		r.clearLine()
	}
	r.job = j
	if !r.running {
		r.running = true
		r.spinDone = make(chan struct{})
		if r.cursorShow {
			_, _ = fmt.Fprint(r.w, "\x1b[?25l")
			r.cursorShow = false
		}
		r.spinWG.Go(r.spin)
	}
	r.draw(true)
}

// spin 按节拍重绘；停止信号只退出循环，清行/光标由持锁调用方完成，避免死锁。
func (r *ProgressBar) spin() {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.spinDone:
			return
		case <-ticker.C:
			r.mu.Lock()
			r.draw(false)
			r.mu.Unlock()
		}
	}
}

// stopSpin 停止转轮并恢复光标，持锁调用。
func (r *ProgressBar) stopSpin(showCursor bool) {
	if r.running {
		close(r.spinDone)
		r.spinWG.Wait()
		r.running = false
	}
	if r.enabled && showCursor && !r.cursorShow {
		_, _ = fmt.Fprint(r.w, "\x1b[?25h")
		r.cursorShow = true
	}
}

// draw 渲染当前帧；force 时忽略节流（计数等实质变化立即反映）。持锁调用。
func (r *ProgressBar) draw(force bool) {
	if !r.enabled || r.job.label == "" {
		return
	}
	if !force && time.Since(r.lastDraw) < progressMinGap {
		return
	}
	r.lastDraw = time.Now()
	payload := r.render()
	padding := ""
	if n := r.lastLen - len(payload); n > 0 {
		padding = strings.Repeat(" ", n)
	}
	_, _ = fmt.Fprint(r.w, "\r\x1b[2K"+payload+padding+"\r")
	r.lastLen = len(payload)
}

// render 当前帧文本（无控制符），持锁调用。
func (r *ProgressBar) render() string {
	j := r.job
	if j.label == "" {
		return ""
	}
	elapsed := formatElapsed(time.Since(j.begin))
	switch {
	case j.total > 0:
		return fmt.Sprintf("%s  %s %d/%d  已用 %s", j.label, renderBar(j.done, j.total), j.done, j.total, elapsed)
	case j.showCount:
		return fmt.Sprintf("%s  %s %d 条  已用 %s", j.label, r.frame(), j.done, elapsed)
	default:
		return fmt.Sprintf("%s  %s  已用 %s", j.label, r.frame(), elapsed)
	}
}

// frame 当前转轮帧并前进一格，持锁调用。
func (r *ProgressBar) frame() string {
	f := spinnerFrames[r.spinIndex%len(spinnerFrames)]
	r.spinIndex++
	return string(f)
}

// clearLine 清除当前进度行（不动光标）。
func (r *ProgressBar) clearLine() {
	if !r.enabled {
		return
	}
	_, _ = fmt.Fprint(r.w, "\r\x1b[2K")
	r.lastLen = 0
}

// renderBar ██░░ 条，宽度恒定。
func renderBar(done, total int64) string {
	filled := min(total, max(0, done)) * progressBarWidth / total
	return strings.Repeat("█", int(filled)) + strings.Repeat("░", progressBarWidth-int(filled))
}

// formatElapsed 已用时：<1 分钟 "3.2s"，否则 "1m05s"，超 1 小时 "1h02m"。
func formatElapsed(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", d/time.Minute, d%time.Minute/time.Second)
	default:
		return fmt.Sprintf("%dh%02dm", d/time.Hour, d%time.Hour/time.Minute)
	}
}
