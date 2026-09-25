package middleware

import (
	"net/http"

	"github.com/example/go-frame/pkg/service/restkit/context"
	"github.com/example/go-frame/pkg/service/restkit/router"
)

// MaxBody 限制请求体大小（字节）。超限后任何读取 body 的动作都会报错，
// 绑定层（context.BindForm）将其统一转成业务错误响应。
// 原配置 rest.requestBodySize 只绑定了 flag 从未接线——任何客户端都可以
// POST 无上限的 body 打爆内存（慢速大包 DoS 面之一）。
func MaxBody(maxBytes int64) router.Handler {
	return func(ctx *context.Context) {
		if ctx.Request.Body != nil {
			ctx.Request.Body = http.MaxBytesReader(ctx.Proxy.Writer, ctx.Request.Body, maxBytes)
		}
		ctx.Proxy.Next()
	}
}
