package middleware

import (
	"slices"
	"strings"

	"github.com/example/go-frame/pkg/service/restkit/context"
	"github.com/example/go-frame/pkg/service/restkit/router"
	"github.com/example/go-frame/pkg/service/tokenkit"
)

// 本文件提供路由级鉴权拦截。链路为：登录态 → 域 → 权限/角色/部门。
// 每个拦截器都自带登录态校验，可直接挂在 router.Group 上独立使用；
// 也可以先用 AuthLogin 统一挡住匿名请求，再叠加更细的拦截器。
//
// 失败一律用 context.ResultAuthErr(401) 响应：本框架的 RestRet 只有 0/401/500 三档，
// 权限不足与登录失效对前端而言同属「重登或联系管理员」，不再单开 403 档位。

// AuthLogin 登录态校验：token 必须能解析出未过期的服务端会话。
// 不传 scopes 表示不校验域（兼容既有 app 端路由）。
//
// 鉴权通过即滑动续期：重置会话 TTL 并刷新在线列表里的最后活跃时间。
// 会话绝对上限（token.expire）以 LoginTime 为基准，续期无法绕过。
func AuthLogin(scopes ...string) router.Handler {
	return func(ctx *context.Context) {
		if !authenticate(ctx, scopes...) {
			return
		}
		ctx.Proxy.Next()
	}
}

// AuthPerm 权限校验：登录 + 拥有给定权限之一（OR 语义）。
// 权限码支持通配，持有 "user:*" 即可通过 "user:add"；持有 "*" 通过一切。
func AuthPerm(perms ...string) router.Handler {
	return func(ctx *context.Context) {
		if !authenticate(ctx) {
			return
		}
		if !ctx.HasPerm(perms...) {
			deny(ctx, "无权限："+strings.Join(perms, "|"))
			return
		}
		ctx.Proxy.Next()
	}
}

// AuthPermAll 权限校验：登录 + 同时拥有全部给定权限（AND 语义）。
func AuthPermAll(perms ...string) router.Handler {
	return func(ctx *context.Context) {
		if !authenticate(ctx) {
			return
		}
		if !ctx.HasPermAll(perms...) {
			deny(ctx, "权限不足："+strings.Join(perms, "|"))
			return
		}
		ctx.Proxy.Next()
	}
}

// AuthRole 角色校验：登录 + 持有给定角色之一（OR 语义）。
func AuthRole(roleIds ...int64) router.Handler {
	return func(ctx *context.Context) {
		if !authenticate(ctx) {
			return
		}
		if !ctx.HasRole(roleIds...) {
			deny(ctx, "无角色权限")
			return
		}
		ctx.Proxy.Next()
	}
}

// AuthDept 部门校验：登录 + 属于给定部门之一（OR 语义）。
// 用户无部门时恒不通过。需要「本部门及子部门」语义时由调用方先展开子树
// （mod/user 的 service.DepartmentsOf）。
func AuthDept(deptIds ...int64) router.Handler {
	return func(ctx *context.Context) {
		if !authenticate(ctx) {
			return
		}
		if !ctx.HasDept(deptIds...) {
			deny(ctx, "无部门权限")
			return
		}
		ctx.Proxy.Next()
	}
}

// authenticate 校验登录态与 token 域，通过则滑动续期；失败直接写 401 并 Abort。
// 返回 false 表示请求已被终结，调用方必须立即 return。
func authenticate(ctx *context.Context, scopes ...string) bool {
	// Refresh 内部会先做存在性/过期判定，并顺带重置 TTL 与在线索引，
	// 一步到位，避免「先 Parse 再单独续期」两次打存储。
	if !tokenkit.Refresh(ctx.GetToken()) {
		deny(ctx, "登录失效")
		return false
	}
	if len(scopes) > 0 {
		s := ctx.GetSession()
		if s == nil || !slices.Contains(scopes, s.Scope) {
			deny(ctx, "登录状态域不匹配，请从对应入口登录")
			return false
		}
	}
	return true
}

// deny 统一写 401 响应并 Abort。
func deny(ctx *context.Context, message string) {
	ctx.Json(context.RestRet{Result: context.ResultAuthErr, Message: message})
	ctx.Proxy.Abort()
}
