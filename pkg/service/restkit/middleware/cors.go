package middleware

import (
	"github.com/example/go-frame/pkg/service/restkit/context"
	"github.com/example/go-frame/pkg/service/restkit/router"
	"net/http"
)

func Cors() router.Handler {
	return func(c *context.Context) {
		method := c.Request.Method
		c.Proxy.Header("Access-Control-Allow-Origin", c.Proxy.Request.Header.Get("Origin"))
		c.Proxy.Header("Access-Control-Allow-Headers", "Content-Type, AccessToken, X-CSRF-Token, Authorization, Accept, Token")
		// 方法表须覆盖 Router 的全部注册能力：原缺 PUT/DELETE，
		// 注册了这两类方法的接口跨域预检（OPTIONS）必失败
		c.Proxy.Header("Access-Control-Allow-Methods", "POST, GET, PUT, DELETE, PATCH, OPTIONS")
		// Max-Age 让浏览器缓存预检结果，避免每次跨域请求都重发 OPTIONS
		c.Proxy.Header("Access-Control-Max-Age", "600")
		c.Proxy.Header("Access-Control-Expose-Headers", "Content-Length, Access-Control-Allow-Origin, Access-Control-Allow-Headers, Content-Type, Authorization,Token")
		c.Proxy.Header("Access-Control-Allow-Credentials", "true")
		// 放行所有OPTIONS方法
		if method == "OPTIONS" {
			c.Proxy.Status(http.StatusNoContent)
			c.Proxy.Abort()
		}
		c.Proxy.Next()
	}
}
