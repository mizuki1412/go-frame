package context

import (
	"net/http"
	"strings"
)

var HeaderTokenKey = "Authorization"
var CookieTokenKey = "token"

// BearerPrefix OpenAPI http/bearer scheme 客户端发送 "Bearer <token>"。
const BearerPrefix = "Bearer "

// TokenCookie 配置项。可在应用启动时通过 configkit 覆盖，或直接赋值。
// - Domain: cookie 作用域名。为空时用请求 Host 的域名。
// - Secure: 是否仅 HTTPS 传输。生产环境应为 true。
// - SameSite: SameSite 策略。跨域场景需 Lax/None（None 时 Secure 必须 true）。
var (
	TokenCookieDomain   = ""
	TokenCookieSecure   = false
	TokenCookieSameSite = http.SameSiteLaxMode
)

// ReadToken 从请求头 Authorization 或 cookie 中取出原始 token 字符串并缓存。
//
// ⚠️ 这里只做「取值」不做「校验」：解析会话要打一次存储，
// 而相当一部分请求是未登录的公开接口（登录、验证码），提前解析纯属浪费。
// 真正需要身份的链路由 GetSession / middleware.AuthLogin 按需触发，且只触发一次。
func (ctx *Context) ReadToken() string {
	if t, ok := ctx.Get(ctxKeyToken).(string); ok {
		return t
	}
	token := ctx.Request.Header.Get(HeaderTokenKey)
	if token == "" || token == "undefined" {
		// 从cookie中获取
		token, _ = ctx.Proxy.Cookie(CookieTokenKey)
	}
	if token == "" || token == "undefined" {
		return ""
	}
	// 兼容 OpenAPI http/bearer scheme：发送 "Bearer <token>"，需剥离前缀
	token = strings.TrimPrefix(token, BearerPrefix)
	ctx.Set(ctxKeyToken, token)
	return token
}
