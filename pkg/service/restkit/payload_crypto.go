package restkit

import (
	"github.com/example/go-frame/pkg/service/restkit/context"
	"github.com/example/go-frame/pkg/service/restkit/middleware"
	"github.com/example/go-frame/pkg/service/restkit/openapi"
)

// PayloadPublicKeyPath 公钥下发端点默认路径
const PayloadPublicKeyPath = "/payload/publicKey"

// EnablePayloadCrypto 启用报文加密能力：注册公钥下发端点（默认 GET /payload/publicKey）。
// 业务路由按需挂载 middleware.DecryptRequest()（请求加密）与 middleware.EncryptResponse()（响应加密），
// 两者同挂时请求与响应共用 X-Enc-Key 头里同一把 AES 密钥。
// 须在 restkit.Run() 之前调用一次；密钥来源见 middleware 包（crypto.rsaPrivateKey 配置或自动生成）。
func EnablePayloadCrypto(path ...string) {
	p := PayloadPublicKeyPath
	if len(path) > 0 && path[0] != "" {
		p = path[0]
	}
	GetRouter().Group(p).Get("", func(ctx *context.Context) {
		ctx.JsonSuccess(map[string]string{"publicKey": middleware.PayloadPublicKey()})
	}).Api(openapi.Tag("restkit:报文加密"), openapi.Summary("获取报文加密公钥"), openapi.Security(nil))
}
