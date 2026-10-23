package cmdkit

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// testWriter 收拢测试输出（bytes.Buffer 非并发安全）。
type testWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *testWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *testWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// newTestProgressBar 走真实构造路径后强制开启并缩短节拍
// （NewProgressBar 的终端检测对非 *os.File 恒为禁用，故覆盖私有字段）。
func newTestProgressBar(w *testWriter) *ProgressBar {
	r := NewProgressBar(w)
	r.enabled = true
	r.cursorShow = true
	r.interval = 2 * time.Millisecond
	return r
}

// TestDisabledSilent 非终端时全 API 静默：零输出、零控制符。
func TestDisabledSilent(t *testing.T) {
	var w testWriter
	r := NewProgressBar(&w)
	r.Stage("探测")
	r.Add(1)
	r.StageSet("拉取", 10)
	r.Finish()
	r.Close()
	if w.String() != "" {
		t.Fatalf("非终端应全局静默，实际输出 %q", w.String())
	}
}

// TestEnvForceEnables 环境变量 CMDKIT_PROGRESS=1 时跳过终端检测强制启用。
func TestEnvForceEnables(t *testing.T) {
	t.Setenv(ProgressEnv, "1")
	var w testWriter
	r := NewProgressBar(&w)
	defer r.Close()
	if !r.enabled {
		t.Fatal("设置环境变量后应强制启用")
	}
	r.Stage("强制")
	if !strings.Contains(w.String(), "强制") {
		t.Fatalf("强制启用后应输出进度帧：%q", w.String())
	}
}

// TestBarFill 条形填充：0 / 一半 / 满 / 超出均正确，总宽恒 20。
func TestBarFill(t *testing.T) {
	cases := []struct {
		done, total int64
		wantFilled  int
	}{
		{0, 10, 0}, {5, 10, 10}, {10, 10, 20}, {20, 10, 20}, {-3, 10, 0},
	}
	for _, c := range cases {
		got := renderBar(c.done, c.total)
		if n := strings.Count(got, "█"); n != c.wantFilled {
			t.Errorf("renderBar(%d, %d) 填充 %d 格，期望 %d：%q", c.done, c.total, n, c.wantFilled, got)
		}
		if n := strings.Count(got, "█") + strings.Count(got, "░"); n != 20 {
			t.Errorf("renderBar(%d, %d) 总宽 %d，期望 20：%q", c.done, c.total, n, got)
		}
	}
}

// TestFormatElapsed 已用时三档格式。
func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0.0s"}, {3200 * time.Millisecond, "3.2s"}, {65 * time.Second, "1m05s"}, {2 * time.Hour, "2h00m"},
	}
	for _, c := range cases {
		if got := formatElapsed(c.d); got != c.want {
			t.Errorf("formatElapsed(%v) = %q，期望 %q", c.d, got, c.want)
		}
	}
}

// TestAddRendersCount 计数累加后帧内各字段齐全（Display 取当前帧，不受重绘节流影响）。
func TestAddRendersCount(t *testing.T) {
	var w testWriter
	r := newTestProgressBar(&w)
	defer r.Close()
	r.Stage("下载快照")
	r.Add(1234)
	frame := r.Display()
	for _, want := range []string{"下载快照", "1234 条", "已用"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("进度帧应包含 %q，实际 %q", want, frame)
		}
	}
}

// TestStageSetRendersBar 已知总数阶段渲染 进度条 + done/total。
func TestStageSetRendersBar(t *testing.T) {
	var w testWriter
	r := newTestProgressBar(&w)
	defer r.Close()
	r.StageSet("写入", 100)
	r.Add(50)
	frame := r.Display()
	for _, want := range []string{"写入", "50/100", "█", "░"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("进度帧应包含 %q，实际 %q", want, frame)
		}
	}
}

// TestAddWithoutStageIgnored 无进行中阶段时 Add 静默忽略，不 panic、不产生输出。
func TestAddWithoutStageIgnored(t *testing.T) {
	var w testWriter
	r := newTestProgressBar(&w)
	defer r.Close()
	r.Add(1)
	if got := w.String(); got != "" {
		t.Fatalf("无阶段时不应输出，实际 %q", got)
	}
}

// TestFinishPrintsDoneLine 完成后打印含耗时与标签的完成行；重复 Finish 不重复打印。
func TestFinishPrintsDoneLine(t *testing.T) {
	var w testWriter
	r := newTestProgressBar(&w)
	r.Stage("探测最近交易日")
	r.Finish()
	r.Finish() // 幂等
	defer r.Close()
	out := w.String()
	if n := strings.Count(out, "完成"); n != 1 {
		t.Fatalf("应恰好打印一次完成行，实际 %d 次：%q", n, out)
	}
	if !strings.Contains(out, "[探测最近交易日] 完成") || !strings.Contains(out, "s\n") {
		t.Fatalf("完成行格式不符：%q", out)
	}
}

// TestStageSwitchWithoutFinish 未 Finish 直接切阶段：帧更新为新标签，不串行、不残留旧行。
func TestStageSwitchWithoutFinish(t *testing.T) {
	var w testWriter
	r := newTestProgressBar(&w)
	defer r.Close()
	r.Stage("旧的")
	r.StageSet("切换阶段", 8)
	if got := r.Display(); !strings.Contains(got, "切换阶段") {
		t.Fatalf("切换后帧应包含新标签，实际 %q", got)
	}
}

// TestCloseIdempotent Close 幂等；关闭后 Add/Finish 静默。
func TestCloseIdempotent(t *testing.T) {
	var w testWriter
	r := newTestProgressBar(&w)
	r.Stage("转轮")
	r.Close()
	r.Close()
	r.Add(1)
	r.Finish()
}

// TestSpinFrames 转轮帧循环推进。
func TestSpinFrames(t *testing.T) {
	r := &ProgressBar{}
	got := ""
	for range 5 {
		got += r.frame()
	}
	if got != `|/-\|` {
		t.Fatalf("转轮帧序列 = %q，期望 %q", got, `|/-\|`)
	}
}

// TestConcurrentAddAndClose 转轮 tick 与 Add 并发执行：无死锁、无 panic，计数正确。
func TestConcurrentAddAndClose(t *testing.T) {
	var w testWriter
	r := newTestProgressBar(&w)
	defer r.Close()
	r.Stage("并发")
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 50 {
				r.Add(1)
			}
		})
	}
	wg.Wait()
	if got := r.Display(); !strings.Contains(got, "200 条") {
		t.Fatalf("并发累加应得 200，实际帧 %q", got)
	}
	r.Finish()
}

// TestCursorLifecycle 转轮期间隐藏光标、Finish 恢复；帧以 \r 原地刷新。
func TestCursorLifecycle(t *testing.T) {
	var w testWriter
	r := newTestProgressBar(&w)
	r.Stage("光标")
	if !strings.Contains(w.String(), "\x1b[?25l") {
		t.Fatalf("Stage 应隐藏光标：%q", w.String())
	}
	r.Finish()
	out := w.String()
	if !strings.Contains(out, "\x1b[?25h") {
		t.Fatalf("Finish 应恢复光标：%q", out)
	}
	if !strings.Contains(out, "\x1b[2K") {
		t.Fatalf("应清除进度行：%q", out)
	}
	r.Close()
}
