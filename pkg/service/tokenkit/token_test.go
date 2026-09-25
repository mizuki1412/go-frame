package tokenkit

import (
	"strconv"
	"testing"
	"time"

	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/library/jsonkit"
	"github.com/example/go-frame/pkg/service/configkit"
)

// newTestTokenkit 把后端切到内存并重置配置，使每个用例互相隔离。
// viper 未 Load 时所有键都未设置，IdleTtl/ExpireTtl 会回落到各自的默认值。
func newTestTokenkit(t *testing.T) {
	t.Helper()
	UseMemoryBackend()
}

// setMultiLogin 覆写 token.multiLogin，用例结束后需还原（viper 是进程级全局）。
func setMultiLogin(t *testing.T, allow bool) {
	t.Helper()
	configkit.Set(configkey.TokenMultiLogin, allow)
}

// marshal 会话记录序列化（测试里要手工改写存储内容，直接复用生产路径的编码方式）。
func marshal(s *Session) string { return jsonkit.ToString(s) }

func TestCreateAndParse(t *testing.T) {
	newTestTokenkit(t)
	token := Create("42", WithScope("app"), WithClient("127.0.0.1", "ua"))
	if token == "" {
		t.Fatal("Create 返回空 token")
	}
	s := Parse(token)
	if s == nil {
		t.Fatal("刚签发的 token 应能解析出会话")
	}
	if s.UserId != "42" {
		t.Errorf("UserId = %q, want %q", s.UserId, "42")
	}
	if s.Scope != "app" {
		t.Errorf("Scope = %q, want %q", s.Scope, "app")
	}
	if s.Ip != "127.0.0.1" || s.UserAgent != "ua" {
		t.Errorf("客户端信息未记录: %+v", s)
	}
	if s.LoginTime.IsZero() || s.LastActive.IsZero() {
		t.Errorf("时间戳未填充: %+v", s)
	}
}

func TestTokenIsUnique(t *testing.T) {
	newTestTokenkit(t)
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		tok := Create("1")
		if _, dup := seen[tok]; dup {
			t.Fatalf("第 %d 次签发出现重复 token", i)
		}
		seen[tok] = struct{}{}
	}
}

func TestParseRejectsUnknownToken(t *testing.T) {
	newTestTokenkit(t)
	if s := Parse("deadbeef"); s != nil {
		t.Errorf("不存在的 token 应返回 nil, got %+v", s)
	}
	if s := Parse(""); s != nil {
		t.Errorf("空 token 应返回 nil, got %+v", s)
	}
}

// 已超绝对上限的会话必须在 Parse 时被判定失效并清理，
// 即使它的空闲 TTL 还没到（滑动续期不能延长绝对上限）。
func TestAbsoluteExpireNotExtendableByRefresh(t *testing.T) {
	newTestTokenkit(t)
	token := Create("7")
	s := Parse(token)
	if s == nil {
		t.Fatal("会话应存在")
	}
	// 把 LoginTime 改到远超绝对上限之前，模拟「一直活跃但活太久」
	s.LoginTime = time.Now().Add(-ExpireTtl() - time.Hour)
	getStore().set(keySessionPrefix+token, []byte(marshal(s)), IdleTtl())

	if _, ok := Refresh(token); ok {
		t.Error("超出绝对上限的会话不应续期成功")
	}
	if got := Parse(token); got != nil {
		t.Errorf("超出绝对上限的会话应失效, got %+v", got)
	}
	if _, total := ListOnline(0, 10); total != 0 {
		t.Errorf("失效会话应从在线索引移除, total = %d", total)
	}
}

func TestDestroySingleSession(t *testing.T) {
	newTestTokenkit(t)
	t1 := Create("9")
	t2 := Create("9")
	if n := OnlineCount("9"); n != 2 {
		t.Fatalf("OnlineCount = %d, want 2", n)
	}
	Destroy(t1)
	if Parse(t1) != nil {
		t.Error("被销毁的 token 不应还能解析")
	}
	if n := OnlineCount("9"); n != 1 {
		t.Errorf("销毁一个后 OnlineCount = %d, want 1", n)
	}
	tokens := ListByUser("9")
	if len(tokens) != 1 || tokens[0] != t2 {
		t.Errorf("剩余会话 = %v, want [%s]", tokens, t2)
	}
}

func TestDestroyByUser(t *testing.T) {
	newTestTokenkit(t)
	Create("9")
	Create("9")
	Create("10")
	if n := DestroyByUser("9"); n != 2 {
		t.Errorf("DestroyByUser 返回 %d, want 2", n)
	}
	if OnlineCount("9") != 0 {
		t.Error("用户 9 应无在线会话")
	}
	if OnlineCount("10") != 1 {
		t.Error("不应影响其他用户")
	}
}

// token.multiLogin=false 时，新登录必须把该用户此前的会话全部顶掉。
func TestSingleLoginKicksPreviousSessions(t *testing.T) {
	newTestTokenkit(t)
	setMultiLogin(t, false)
	defer setMultiLogin(t, true)

	first := Create("5")
	second := Create("5")

	if Parse(first) != nil {
		t.Error("单端登录下旧会话应被顶掉")
	}
	if Parse(second) == nil {
		t.Error("最新会话应有效")
	}
	if n := OnlineCount("5"); n != 1 {
		t.Errorf("OnlineCount = %d, want 1", n)
	}
}

func TestRefreshUpdatesLastActive(t *testing.T) {
	newTestTokenkit(t)
	token := Create("3")
	before := LastActive(token)
	time.Sleep(1100 * time.Millisecond)
	s, ok := Refresh(token)
	if !ok {
		t.Fatal("Refresh 应成功")
	}
	if !s.LastActive.After(before) {
		t.Errorf("Refresh 返回的会话 LastActive 未推进: before=%s after=%s", before, s.LastActive)
	}
	// LastActive 的实时权威在在线索引 score，Refresh 必须推进它
	if got := LastActive(token); !got.After(before) {
		t.Errorf("在线索引 LastActive 未推进: %s", got)
	}
}

// 写节流：距上次落库不足空闲窗口一半时，Refresh 只推进在线索引 score，
// 不重写会话记录（记录里的 LastActive 允许滞后）。
func TestRefreshThrottlesRecordWrite(t *testing.T) {
	newTestTokenkit(t)
	token := Create("3")
	before := Parse(token).LastActive
	time.Sleep(1100 * time.Millisecond)
	if _, ok := Refresh(token); !ok {
		t.Fatal("Refresh 应成功")
	}
	if after := Parse(token).LastActive; !after.Equal(before) {
		t.Errorf("窗口内不应重写会话记录: before=%s after=%s", before, after)
	}
}

// 距上次落库超过空闲窗口一半时，Refresh 必须重写会话记录（顺带拉满 TTL）。
func TestRefreshRewritesRecordBeyondHalfWindow(t *testing.T) {
	newTestTokenkit(t)
	token := Create("3")
	s := Parse(token)
	// 空闲窗口默认 168h，把记录的 LastActive 拨到窗口一半之前，模拟久未落库
	s.LastActive = time.Now().Add(-IdleTtl() - time.Minute)
	getStore().set(keySessionPrefix+token, []byte(marshal(s)), IdleTtl())
	if _, ok := Refresh(token); !ok {
		t.Fatal("Refresh 应成功")
	}
	if after := Parse(token).LastActive; time.Since(after) > time.Minute {
		t.Errorf("超窗口一半应重写会话记录: LastActive=%s", after)
	}
}

// 在线列表按最后活跃时间倒序，且 total 反映清理后的真实在线数。
func TestListOnlineOrderAndTotal(t *testing.T) {
	newTestTokenkit(t)
	first := Create("1")
	time.Sleep(1100 * time.Millisecond)
	second := Create("2")

	list, total := ListOnline(0, 10)
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if len(list) != 2 {
		t.Fatalf("列表长度 = %d, want 2", len(list))
	}
	if list[0].Token != second || list[1].Token != first {
		t.Errorf("未按最近活跃倒序: %s, %s", list[0].Token, list[1].Token)
	}
	if list[0].UserId != "2" {
		t.Errorf("首条 UserId = %q, want %q", list[0].UserId, "2")
	}
}

func TestListOnlinePaging(t *testing.T) {
	newTestTokenkit(t)
	var tokens []string
	for i := 0; i < 5; i++ {
		tokens = append(tokens, Create(strconv.Itoa(i+1)))
	}
	// 逆序 Refresh 且每次间隔 5ms：最后刷新的 tokens[0] 最新，倒序即正序
	for i := len(tokens) - 1; i >= 0; i-- {
		_, _ = Refresh(tokens[i])
		time.Sleep(5 * time.Millisecond)
	}
	page1, total := ListOnline(0, 2)
	page2, _ := ListOnline(2, 2)
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(page1) != 2 || len(page2) != 2 {
		t.Fatalf("分页长度 = %d, %d, want 2, 2", len(page1), len(page2))
	}
	if page1[0].Token != tokens[0] || page1[1].Token != tokens[1] {
		t.Errorf("第一页 = %s,%s, want %s,%s", page1[0].Token, page1[1].Token, tokens[0], tokens[1])
	}
	if page2[0].Token != tokens[2] {
		t.Errorf("第二页首条 = %s, want %s", page2[0].Token, tokens[2])
	}
}

func TestListOnlineZeroLimitReturnsTotalOnly(t *testing.T) {
	newTestTokenkit(t)
	Create("1")
	Create("2")
	list, total := ListOnline(0, 0)
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(list) != 0 {
		t.Errorf("limit=0 时不应返回条目, got %d", len(list))
	}
}

func TestListOnlineOf(t *testing.T) {
	newTestTokenkit(t)
	Create("1")
	Create("1")
	Create("2")
	if n := len(ListOnlineOf("1")); n != 2 {
		t.Errorf("用户 1 的在线会话数 = %d, want 2", n)
	}
}

func TestDestroyIdempotent(t *testing.T) {
	newTestTokenkit(t)
	token := Create("1")
	Destroy(token)
	Destroy(token) // 重复销毁不得 panic
	Destroy("")    // 空 token 静默返回
	DestroyByUser("999")
}

// 用户 id 是不透明字符串：本包不假设它是数值，UUID/雪花串应能正常建索引与剔出。
func TestOpaqueUserId(t *testing.T) {
	newTestTokenkit(t)
	const uid = "550e8400-e29b-41d4-a716-446655440000"
	token := Create(uid)
	s := Parse(token)
	if s == nil || s.UserId != uid {
		t.Fatalf("非数字 uid 应原样保留: %+v", s)
	}
	if got := ListByUser(uid); len(got) != 1 || got[0] != token {
		t.Errorf("ListByUser = %v, want [%s]", got, token)
	}
	if n := DestroyByUser(uid); n != 1 {
		t.Errorf("DestroyByUser = %d, want 1", n)
	}
	if Parse(token) != nil {
		t.Error("剔出后会话应失效")
	}
}

// 空 id 会把用户索引写成 tk:u: 这种悬空 key，且语义上也不成立，Create 必须拒绝。
func TestCreateRejectsEmptyUserId(t *testing.T) {
	newTestTokenkit(t)
	defer func() {
		if recover() == nil {
			t.Error("空 userId 应 panic")
		}
	}()
	Create("")
}

func TestUserScopedApisRejectEmptyId(t *testing.T) {
	newTestTokenkit(t)
	if n := DestroyByUser(""); n != 0 {
		t.Errorf("DestroyByUser(\"\") = %d, want 0", n)
	}
	if got := ListByUser(""); got != nil {
		t.Errorf("ListByUser(\"\") = %v, want nil", got)
	}
	if n := OnlineCount(""); n != 0 {
		t.Errorf("OnlineCount(\"\") = %d, want 0", n)
	}
}
