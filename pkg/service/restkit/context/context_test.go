package context

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/example/go-frame/pkg/class/exception"
	"github.com/gin-gonic/gin"
)

// newTestContext 构造带请求的 gin 测试上下文。
func newTestContext(t *testing.T, method, target, body, contentType string) *Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	c.Request = r
	return &Context{Proxy: c, Request: r, Response: c.Writer}
}

type bindMergeReq struct {
	Username string `json:"username"`
	Page     int    `json:"page"`
	Limit    int    `json:"limit" default:"20"`
}

// JSON body 与 query 同时绑定：body 之外的字段从 query 取，同名字段 body 优先，
// default tag 在 JSON 请求下同样生效（修复前 JSON 路径直接 return，query/default 全部丢失）。
func TestBindStructJsonMergesQuery(t *testing.T) {
	ctx := newTestContext(t, "POST", "/x?page=9", `{"username":"u"}`, "application/json")
	bean := &bindMergeReq{}
	ctx.bindStruct(bean)
	if bean.Username != "u" {
		t.Errorf("Username = %q, want u", bean.Username)
	}
	if bean.Page != 9 {
		t.Errorf("query 的 page 应被绑定, got %d", bean.Page)
	}
	if bean.Limit != 20 {
		t.Errorf("default tag 应生效, got %d", bean.Limit)
	}
}

func TestBindStructJsonOverridesQuery(t *testing.T) {
	ctx := newTestContext(t, "POST", "/x?page=9", `{"username":"u","page":2}`, "application/json")
	bean := &bindMergeReq{}
	ctx.bindStruct(bean)
	if bean.Page != 2 {
		t.Errorf("body 应优先于 query, got %d", bean.Page)
	}
}

// form 路径回归：原有 PostForm/Query/Param 合并行为不变。
func TestBindStructFormPath(t *testing.T) {
	ctx := newTestContext(t, "POST", "/x?page=3", "username=f", "application/x-www-form-urlencoded")
	bean := &bindMergeReq{}
	ctx.bindStruct(bean)
	if bean.Username != "f" || bean.Page != 3 {
		t.Errorf("form/query 绑定失败: %+v", bean)
	}
}

// body 读后必须回填：ShouldBindJSON 才能再次读取（gin v1.12 的 GetRawData 不回填，
// 这里验证手动回填路径——若回填失效，username 将无法绑定）。
func TestBindStructBodyRewoundForBind(t *testing.T) {
	ctx := newTestContext(t, "POST", "/x", `{"username":"u","page":1}`, "application/json")
	bean := &bindMergeReq{}
	snapshot := ctx.bindStruct(bean)
	if bean.Username != "u" || bean.Page != 1 {
		t.Fatalf("回填后绑定失败: %+v", bean)
	}
	if !strings.Contains(snapshot, `"username":"u"`) {
		t.Errorf("快照应含原始 body, got %q", snapshot)
	}
}

// 请求参数快照：form 路径为 key=value 对，敏感字段掩码。
func TestBindStructSnapshotMasksSensitive(t *testing.T) {
	type req struct {
		Username string `json:"username"`
		Pwd      string `json:"pwd"`
	}
	ctx := newTestContext(t, "POST", "/x", "username=f&pwd=secret", "application/x-www-form-urlencoded")
	snapshot := ctx.bindStruct(&req{})
	if !strings.Contains(snapshot, "username=f") {
		t.Errorf("快照应含普通参数, got %q", snapshot)
	}
	if strings.Contains(snapshot, "secret") {
		t.Errorf("敏感字段值不应出现在快照中, got %q", snapshot)
	}
	if !strings.Contains(snapshot, "pwd=******") {
		t.Errorf("敏感字段应被掩码, got %q", snapshot)
	}
}

// 超过 MaxBytesReader 上限的 JSON body 应给出明确业务错误，而非裸的解析失败。
func TestBindStructBodyTooLarge(t *testing.T) {
	ctx := newTestContext(t, "POST", "/x", `{"username":"u"}`, "application/json")
	small := strings.NewReader(`{"username":"u"}`)
	ctx.Request.Body = http.MaxBytesReader(ctx.Proxy.Writer, io.NopCloser(small), 2)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("超限 body 应 panic")
		}
		ex, ok := r.(exception.Exception)
		if !ok {
			t.Fatalf("应 panic 出 Exception, got %T", r)
		}
		if !strings.Contains(ex.Msg, "请求体超过大小限制") {
			t.Errorf("应报大小限制错误, got %q", ex.Msg)
		}
	}()
	ctx.bindStruct(&bindMergeReq{})
}

func TestMaskHelpers(t *testing.T) {
	if got := maskValue("Password", "x"); got != "******" {
		t.Errorf("Password 应掩码, got %q", got)
	}
	if got := maskValue("username", "x"); got != "x" {
		t.Errorf("普通字段不应掩码, got %q", got)
	}
	masked := maskJSONBody(`{"pwd":"abc","nested":{"password":"x"},"name":"我"}`)
	if strings.Contains(masked, "abc") || strings.Contains(masked, `"x"`) {
		t.Errorf("JSON 敏感值应掩码: %s", masked)
	}
	if !strings.Contains(masked, `"name":"我"`) {
		t.Errorf("普通 JSON 字段应保留: %s", masked)
	}
}

// 截断落在多字节字符中间时不应产生非法 UTF-8（中文参数日志常见）。
func TestTruncateLogBodyValidUTF8(t *testing.T) {
	s := strings.Repeat("中", 600) // 1800 字节，超过 1024
	got := truncateLogBody(s)
	if len(got) > logBodyMaxSize {
		t.Errorf("应截断到上限内, len=%d", len(got))
	}
	if !utf8.ValidString(got) {
		t.Error("截断后应仍为合法 UTF-8")
	}
}
