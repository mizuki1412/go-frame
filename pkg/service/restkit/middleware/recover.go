package middleware

import (
	"errors"

	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/service/logkit"
	"github.com/example/go-frame/pkg/service/restkit/context"
	"github.com/example/go-frame/pkg/service/restkit/router"
	"github.com/spf13/cast"
)

// Recover 错误处理。
// B4: 用 Writer.Written() 判断响应是否已写入，替代 IsAborted()。
// 原逻辑在 IsAborted() 为 true 时直接 return，会吞掉未写入响应的错误：
//   - handler 调用 Abort() 后 panic → 响应未写 → return 后客户端拿到空响应
//
// 改为：只要响应未写入就补写 JsonError，已写入则跳过避免 gin 重复写告警。
// S30: errors.As 兼容 Exception 及其包装链；业务码非零时透传到响应 result，
// 未指定(0)仍按 500 处理，保持历史行为不变。
func Recover() router.Handler {
	return func(ctx *context.Context) {
		defer func() {
			if err := recover(); err != nil {
				var msg string
				var code = exception.CodeNone
				var e exception.Exception
				// panic 日志带上 url/method，与访问日志可按请求关联
				loc := []any{"url", ctx.Request.URL.Path, "method", ctx.Request.Method}
				if errors.As(errToError(err), &e) {
					msg = e.Msg
					code = e.Code
					// 带代码位置信息
					logkit.ErrorException(e, loc...)
				} else {
					msg = cast.ToString(err)
					logkit.ErrorException(exception.New(msg, 3), loc...)
				}
				if !ctx.Proxy.Writer.Written() {
					if code == exception.CodeNone || code == context.ResultErr {
						ctx.JsonError(msg)
					} else {
						ctx.JsonErrorCode(code, msg)
					}
				}
			}
		}()
		ctx.Proxy.Next()
	}
}

// errToError 把 panic value 归一为 error 以便走 errors.As 链路判定。
func errToError(v any) error {
	if e, ok := v.(error); ok {
		return e
	}
	return castError{v}
}

type castError struct{ v any }

func (c castError) Error() string { return cast.ToString(c.v) }
