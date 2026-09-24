package context

import (
	"strings"
	"time"

	"github.com/example/go-frame/pkg/service/tokenkit"
	"github.com/spf13/cast"
)

// gin.Context 中的缓存 key。
const (
	ctxKeyToken   = "token"
	ctxKeySession = "token-session"
)

// SetTokenCookie 把登录时签发的 token 写入 cookie。
//
// 会话记录本身已由 tokenkit.Create 落库，这里只负责浏览器侧的持有。
// expireAt 为 cookie 的存活上限，通常传 tokenkit.ExpireTtl() 时的时间点。
func (ctx *Context) SetTokenCookie(token string, expireAt time.Time) {
	// B3: 从配置读取 cookie domain/secure/samesite，避免从 Origin 头推断导致跨域 cookie 失效
	domain := TokenCookieDomain
	origin := ctx.Proxy.GetHeader("origin")
	origins := strings.Split(origin, "//")
	if len(origins) > 1 {
		origin = origins[1]
	}
	// 剥离端口：cookie Domain 不允许含端口
	if idx := strings.Index(origin, ":"); idx > 0 {
		origin = origin[:idx]
	}
	// 配置未指定 domain 时，用请求 Host 的域名（剥端口）
	if domain == "" {
		domain = origin
	}
	maxAge := 0
	if !expireAt.IsZero() {
		maxAge = int(time.Until(expireAt).Seconds())
		if maxAge < 0 {
			maxAge = 0
		}
	}
	// B3: 显式构造 http.Cookie，支持 SameSite/Secure
	ctx.Proxy.SetSameSite(TokenCookieSameSite)
	ctx.Proxy.SetCookie(CookieTokenKey, token, maxAge, "/", domain, TokenCookieSecure, true)
}

// GetToken 返回登录时签发的原始 token 字符串（惰性读取，请求内只解析一次）。
//
// ⚠️ 必须用这个原始字符串做会话查询/销毁，不能重新签发——它是服务端会话的主键。
func (ctx *Context) GetToken() string {
	return ctx.ReadToken()
}

// GetSession 解析并返回当前登录会话，无效会话（不存在/已过期/已剔出）返回 nil。
// 解析结果缓存在请求上下文中，多次调用只打一次存储。
//
// 本方法只读不续期；滑动续期由 middleware.AuthLogin 在鉴权通过后统一做。
func (ctx *Context) GetSession() *tokenkit.Session {
	if s, ok := ctx.Get(ctxKeySession).(*tokenkit.Session); ok {
		return s
	}
	token := ctx.GetToken()
	if token == "" {
		return nil
	}
	s := tokenkit.Parse(token)
	// 缓存结果（含 nil）：未登录请求后续再取不必重复解析
	ctx.Set(ctxKeySession, s)
	return s
}

// GetUidStr 当前登录用户 id（不透明字符串），未登录返回空串。
// 这是取用户身份的**首选入口**：tokenkit 不对 id 类型做任何假设，
// 用字符串才能同时容纳自增 bigint、雪花 id 与 UUID。
func (ctx *Context) GetUidStr() string {
	if s := ctx.GetSession(); s != nil {
		return s.UserId
	}
	return ""
}

// GetUid 把当前登录用户 id 转成 int64，未登录返回 0。
//
// 仅供用户 id 确实是数值型的业务层使用（如 mod/user 的 sys_user.id bigint）；
// id 为非数字串时返回 0——**调用方须自行校验**，否则会把「解析失败」当成
// 「用户 0」继续执行。若你的 id 不是数值型，请一律用 GetUidStr。
func (ctx *Context) GetUid() int64 {
	return cast.ToInt64(ctx.GetUidStr())
}

// IsLogin 当前请求是否携带有效登录会话。
func (ctx *Context) IsLogin() bool { return ctx.GetSession() != nil }

// RefreshToken 续期当前会话（滑动窗口重置），返回是否续期成功。
// 供「刷新 token」类接口使用。
func (ctx *Context) RefreshToken() bool {
	token := ctx.GetToken()
	if token == "" {
		return false
	}
	return tokenkit.Refresh(token)
}

// DestroyToken 销毁当前会话（登出），并清掉请求内的缓存。
func (ctx *Context) DestroyToken() {
	token := ctx.GetToken()
	if token == "" {
		return
	}
	tokenkit.Destroy(token)
	ctx.Set(ctxKeySession, (*tokenkit.Session)(nil))
}

// ClientIp 客户端 IP（已按 rest.trustedProxies 校正 X-Forwarded-For）。
func (ctx *Context) ClientIp() string { return ctx.Proxy.ClientIP() }

// UserAgent 客户端 UA。
func (ctx *Context) UserAgent() string { return ctx.Request.UserAgent() }

// SessionOptions 登录时传给 tokenkit.Create 的会话选项，填好客户端信息。
func (ctx *Context) SessionOptions(scope string) []tokenkit.Option {
	return []tokenkit.Option{
		tokenkit.WithScope(scope),
		tokenkit.WithClient(ctx.ClientIp(), ctx.UserAgent()),
	}
}
