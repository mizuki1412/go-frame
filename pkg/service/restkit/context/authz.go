package context

import (
	"strings"

	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/service/tokenkit"
)

// 本文件是「当前登录用户能做什么」的函数级入口，与 middleware 的路由级拦截互补：
// middleware.AuthPerm 拦的是「这个接口能不能进」，这里拦的是「这次操作能不能做」，
// 后者适合写在 handler 的分支里，或把 Principal 透传给 service 做行级/字段级判定。

// Principal 返回当前登录用户的权限主体。
// 未登录、或业务模块尚未调用 tokenkit.SetLoader 时返回 nil——调用方须按「无权限」处理。
//
// 传给 service 层时用 *tokenkit.Principal 而不是本 Context：
// service 不应接触 HTTP 语义的上下文（见 AGENTS.md 分层约定）。
func (ctx *Context) Principal() *tokenkit.Principal {
	return tokenkit.PrincipalOf(ctx.GetUidStr())
}

// HasPerm 是否拥有给定权限之一（OR 语义，支持 "user:*" 通配）。
func (ctx *Context) HasPerm(perms ...string) bool {
	return ctx.Principal().HasPerm(perms...)
}

// HasPermAll 是否同时拥有全部给定权限（AND 语义，支持 "user:*" 通配）。
func (ctx *Context) HasPermAll(perms ...string) bool {
	p := ctx.Principal()
	if p == nil {
		return false
	}
	for _, perm := range perms {
		if !p.HasPrivilege(perm) {
			return false
		}
	}
	return len(perms) > 0
}

// HasRole 是否持有给定角色之一。
func (ctx *Context) HasRole(roleIds ...int64) bool {
	return ctx.Principal().HasRole(roleIds...)
}

// HasDept 是否属于给定部门之一。
func (ctx *Context) HasDept(deptIds ...int64) bool {
	return ctx.Principal().HasDept(deptIds...)
}

// MustPerm 无权限时直接 panic 出业务错误，由 middleware.Recover 转成错误响应。
// 适合 handler 内做「前置条件不满足就不必继续」的场景。
func (ctx *Context) MustPerm(perms ...string) {
	if !ctx.HasPerm(perms...) {
		panic(exception.New("无权限：" + strings.Join(perms, "|")))
	}
}
