package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/example/go-frame/pkg/service/tokenkit"
)

// OnlineSession 的 UserId 有内外两层同名成员：内层是 tokenkit 的 string 版，
// 外层是 int64 版。这是有意为之（encoding/json 按深度就近取字段，外层胜出），
// 目的是让在线列表的 userId 与用户模块其余接口的数字类型保持一致。
// 本用例把该 JSON 契约钉死——若哪天有人"清理"掉外层字段，这里会立刻失败。
func TestOnlineSessionUserIdIsNumeric(t *testing.T) {
	s := OnlineSession{
		Online: tokenkit.Online{
			Session: tokenkit.Session{UserId: "42", Scope: "app"},
			Token:   "tok-1",
		},
		UserId: 42,
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, `"userId":42`) {
		t.Errorf("userId 应序列化为数字 42，实际: %s", got)
	}
	if strings.Contains(got, `"userId":"42"`) {
		t.Errorf("内层 string 版 UserId 不应出现在 JSON 中，实际: %s", got)
	}
	// 内层的其他字段仍应透传，确认遮蔽只影响同名成员
	if !strings.Contains(got, `"token":"tok-1"`) || !strings.Contains(got, `"scope":"app"`) {
		t.Errorf("内层字段应正常透传，实际: %s", got)
	}
}

// 反序列化时外层数字 id 优先，且不会因为内层 string 字段类型不符而报错。
func TestOnlineSessionUnmarshal(t *testing.T) {
	var s OnlineSession
	err := json.Unmarshal([]byte(`{"userId":7,"token":"tok","loginTime":"2026-01-01T00:00:00Z"}`), &s)
	if err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}
	if s.UserId != 7 {
		t.Errorf("UserId = %d, want 7", s.UserId)
	}
	if s.Token != "tok" {
		t.Errorf("Token = %q, want %q", s.Token, "tok")
	}
	if s.LoginTime.IsZero() {
		t.Error("LoginTime 应经由内层 Session 反序列化")
	}
	if !s.LoginTime.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("LoginTime = %s", s.LoginTime)
	}
}
