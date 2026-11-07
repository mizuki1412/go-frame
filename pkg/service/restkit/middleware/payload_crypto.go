package middleware

import (
	"bytes"
	"encoding/json/v2"
	"io"
	"net/http"
	"sync"

	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/library/cryptokit"
	"github.com/example/go-frame/pkg/service/configkit"
	"github.com/example/go-frame/pkg/service/logkit"
	"github.com/example/go-frame/pkg/service/restkit/context"
	"github.com/example/go-frame/pkg/service/restkit/router"
	"github.com/gin-gonic/gin"
)

// 报文加密协议（RSA + AES 混合）：
//
//	公钥下发  GET /payload/publicKey → {result:0, data:{publicKey:"<PKIX PEM>"}}（restkit.EnablePayloadCrypto 注册）
//	密钥头    X-Enc-Key: base64( RSA( base64(64B 密钥材料) ) )，64B = AESKey(32B) || MACKey(32B)，前端每请求随机生成；
//	          内层再编码为 base64 串是因 jsencrypt 按 charCode 做 UTF-8 处理，二进制密钥不可直传（WebCrypto 前端可直传裸 64B，两者均兼容）
//	加密体    {"body":"base64( IV(16B) || AES-256-CBC-PKCS7(明文) || MAC(32B) )"}，MAC = HMAC-SHA256(MACKey, IV||密文)
//
// DecryptRequest 解密请求体后重写 Request.Body，业务层 ctx.BindForm 无感知；
// EncryptResponse 将 handler 写出的响应体整体加密。两个中间件同挂时共用 X-Enc-Key 里同一把密钥。

// EncKeyHeader 请求头：RSA 公钥加密的 AES 密钥材料（base64），请求/响应加密共用
const EncKeyHeader = "X-Enc-Key"

// EncPayloadHeader 响应头：标记响应体已按信封格式加密
const EncPayloadHeader = "X-Enc-Payload"

// payloadEnvelope 加密信封：body 为 base64(IV||密文||MAC)
type payloadEnvelope struct {
	Body string `json:"body"`
}

// payloadKeys 报文加密 RSA 密钥对（PEM）
type payloadKeys struct {
	privPem string
	pubPem  string
}

// getPayloadKeys 懒加载密钥对：优先取 crypto.rsaPrivateKey 配置（PKCS#8 PEM，支持 ${ENV} 占位）；
// 未配置则自动生成 2048 位临时密钥——前端每请求现取公钥，重启换钥不影响使用；
// 多实例部署必须配置统一私钥，否则各实例公钥不一致导致解密失败。
var getPayloadKeys = sync.OnceValue(func() payloadKeys {
	if pemStr := configkit.GetString(configkey.CryptoRsaPrivateKey, ""); pemStr != "" {
		pub, err := cryptokit.RSAPublicFromPrivate(pemStr)
		if err != nil {
			logkit.Fatal("crypto.rsaPrivateKey 配置无效", "err", err.Error())
		}
		return payloadKeys{privPem: pemStr, pubPem: pub}
	}
	priv, pub, err := cryptokit.GenerateRSAKeyPair(2048)
	if err != nil {
		logkit.Fatal("报文加密密钥生成失败", "err", err.Error())
	}
	logkit.Info("crypto.rsaPrivateKey 未配置，已自动生成临时密钥（多实例部署请配置统一私钥）")
	return payloadKeys{privPem: priv, pubPem: pub}
})

// PayloadPrivateKey 报文加密 RSA 私钥（PEM）。
func PayloadPrivateKey() string {
	return getPayloadKeys().privPem
}

// PayloadPublicKey 报文加密 RSA 公钥（PEM）。
func PayloadPublicKey() string {
	return getPayloadKeys().pubPem
}

// decryptKeyMaterial 解析 X-Enc-Key 头得到 64B AES 密钥材料。
// 兼容两种前端封装：裸 64B（WebCrypto 直传）与 base64 串（jsencrypt 仅支持 ASCII 入参）。
// 缺头或解密失败时写错误响应并 Abort，返回 false。
func decryptKeyMaterial(ctx *context.Context) ([]byte, bool) {
	header := ctx.Proxy.GetHeader(EncKeyHeader)
	if header == "" {
		ctx.JsonError("缺少 " + EncKeyHeader + " 加密密钥头")
		ctx.Proxy.Abort()
		return nil, false
	}
	key, err := cryptokit.RSADecrypt(PayloadPrivateKey(), cryptokit.BytesDecode(header))
	if err == nil && len(key) != cryptokit.AuthAESKeyLen {
		// jsencrypt 路径：解出的是「64B 密钥材料的 base64 串」，补一次解码
		if dec := cryptokit.BytesDecode(string(key)); len(dec) == cryptokit.AuthAESKeyLen {
			key = dec
		}
	}
	if err != nil || len(key) != cryptokit.AuthAESKeyLen {
		logkit.Error("报文加密密钥解析失败", "url", ctx.Request.URL.Path)
		ctx.JsonError("报文解密失败")
		ctx.Proxy.Abort()
		return nil, false
	}
	return key, true
}

// abortErr 写错误响应并中止链路
func abortErr(ctx *context.Context, msg string) {
	logkit.Error(msg, "url", ctx.Request.URL.Path)
	ctx.JsonError(msg)
	ctx.Proxy.Abort()
}

// DecryptRequest 请求报文解密中间件（挂载即强制）。
// 前端发送密文体（信封）+ X-Enc-Key 密钥头，本中间件解密后重写 Request.Body
// 并置 Content-Type: application/json，后续 ctx.BindForm 无感知拿到明文参数，
// 请求日志快照与敏感字段掩码照常作用于解密后明文。
func DecryptRequest() router.Handler {
	return func(ctx *context.Context) {
		key, ok := decryptKeyMaterial(ctx)
		if !ok {
			return
		}
		raw, err := io.ReadAll(ctx.Request.Body)
		if err != nil {
			abortErr(ctx, "请求体读取失败")
			return
		}
		var env payloadEnvelope
		if err := json.Unmarshal(raw, &env); err != nil || env.Body == "" {
			abortErr(ctx, "加密报文格式错误")
			return
		}
		plain, err := cryptokit.AuthAESDecrypt(key, cryptokit.BytesDecode(env.Body))
		if err != nil {
			abortErr(ctx, "报文解密失败")
			return
		}
		ctx.Request.Header.Set("Content-Type", "application/json")
		ctx.Request.Body = io.NopCloser(bytes.NewReader(plain))
		ctx.Proxy.Next()
	}
}

// EncryptResponse 响应报文加密中间件（挂载即强制）。
// 前端在请求头 X-Enc-Key 携带 RSA 加密的 64B 密钥材料（与 DecryptRequest 同挂时共用同一把，
// 请求响应都加密）；handler 写出的响应体整体加密为信封并置响应头 X-Enc-Payload: 1。
// 期间 panic 时丢弃缓冲并上抛，由 Recover 补写明文错误信封（前端无需解密即可读取错误提示）。
// 不适用于 SSE / 文件下载等流式或非 JSON 响应。
func EncryptResponse() router.Handler {
	return func(ctx *context.Context) {
		key, ok := decryptKeyMaterial(ctx)
		if !ok {
			return
		}
		orig := ctx.Proxy.Writer
		w := &bufferWriter{ResponseWriter: orig}
		ctx.Proxy.Writer = w
		defer func() {
			ctx.Proxy.Writer = orig
			if r := recover(); r != nil {
				// panic：丢弃缓冲恢复 writer，原样上抛给 Recover 处理
				panic(r)
			}
			flushEncryptedResponse(orig, w, key)
		}()
		ctx.Proxy.Next()
	}
}

// flushEncryptedResponse 加密缓冲内容并写出到真实 writer
func flushEncryptedResponse(orig gin.ResponseWriter, w *bufferWriter, key []byte) {
	body := w.buf.Bytes()
	if len(body) == 0 {
		// handler 未写响应：按原状态码放行，不做加密
		orig.WriteHeader(w.Status())
		return
	}
	enc, err := cryptokit.AuthAESEncrypt(key, body)
	if err != nil {
		logkit.Error("响应报文加密失败", "err", err.Error())
		orig.WriteHeader(http.StatusInternalServerError)
		return
	}
	out, err := json.Marshal(payloadEnvelope{Body: cryptokit.BytesEncode(enc)})
	if err != nil {
		logkit.Error("响应报文编码失败", "err", err.Error())
		orig.WriteHeader(http.StatusInternalServerError)
		return
	}
	orig.Header().Set(EncPayloadHeader, "1")
	orig.WriteHeader(w.Status())
	_, _ = orig.Write(out)
}

// bufferWriter 响应缓冲 writer：拦截 handler 的写出，交给 EncryptResponse 统一加密后落盘。
// Header() 沿用底层共享 header map，handler 设置的 Content-Type/Cookie 等照常生效。
type bufferWriter struct {
	gin.ResponseWriter
	buf    bytes.Buffer
	status int
}

func (w *bufferWriter) Write(b []byte) (int, error) {
	return w.buf.Write(b)
}

func (w *bufferWriter) WriteString(s string) (int, error) {
	return w.buf.WriteString(s)
}

func (w *bufferWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func (w *bufferWriter) WriteHeaderNow() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
}

func (w *bufferWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *bufferWriter) Size() int {
	return w.buf.Len()
}

func (w *bufferWriter) Written() bool {
	return true
}

// Flush 缓冲期间不向客户端半发（加密必须整体完成后一次写出）
func (w *bufferWriter) Flush() {}
