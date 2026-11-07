package middleware

import (
	"crypto/rand"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/library/cryptokit"
	"github.com/example/go-frame/pkg/service/configkit"
	"github.com/example/go-frame/pkg/service/restkit/context"
	"github.com/example/go-frame/pkg/service/restkit/router"
	"github.com/gin-gonic/gin"
)

// 测试密钥只生成一次（RSA 密钥生成较慢），注入 configkit 供密钥懒加载使用
var (
	testKeyOnce   sync.Once
	testPrivPem   string
	testPubPem    string
	testKeyMatrix []byte
)

func setupKeys(t *testing.T) {
	t.Helper()
	testKeyOnce.Do(func() {
		priv, pub, err := cryptokit.GenerateRSAKeyPair(2048)
		if err != nil {
			t.Fatalf("生成密钥对失败: %v", err)
		}
		testPrivPem, testPubPem = priv, pub
		testKeyMatrix = make([]byte, cryptokit.AuthAESKeyLen)
		if _, err := rand.Read(testKeyMatrix); err != nil {
			t.Fatal(err)
		}
		configkit.Set(configkey.CryptoRsaPrivateKey, testPrivPem)
	})
}

// wrapHandler 复刻 router.handlerTransOne 的包装方式
func wrapHandler(h router.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		h(&context.Context{Proxy: c, Request: c.Request, Response: c.Writer})
	}
}

// encRequest 按协议构造加密请求体与 X-Enc-Key 头（jsencrypt 形态：RSA 加密「64B 密钥材料的 base64 串」）
func encRequest(t *testing.T, keyMaterial []byte, plaintext []byte) (headerValue string, body []byte) {
	t.Helper()
	headerValue = cryptokit.BytesEncode(mustRSAEncrypt(t, testPubPem, []byte(cryptokit.BytesEncode(keyMaterial))))
	enc, err := cryptokit.AuthAESEncrypt(keyMaterial, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	body, err = json.Marshal(payloadEnvelope{Body: cryptokit.BytesEncode(enc)})
	if err != nil {
		t.Fatal(err)
	}
	return headerValue, body
}

func mustRSAEncrypt(t *testing.T, pubPem string, plaintext []byte) []byte {
	t.Helper()
	ct, err := cryptokit.RSAEncrypt(pubPem, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	return ct
}

// decResponseBody 解析加密响应体为明文字节
func decResponseBody(t *testing.T, keyMaterial []byte, respBody []byte) []byte {
	t.Helper()
	var env payloadEnvelope
	if err := json.Unmarshal(respBody, &env); err != nil {
		t.Fatalf("响应不是信封格式: %s", respBody)
	}
	plain, err := cryptokit.AuthAESDecrypt(keyMaterial, cryptokit.BytesDecode(env.Body))
	if err != nil {
		t.Fatalf("响应解密失败: %v", err)
	}
	return plain
}

// doRequest 执行一次请求
func doRequest(t *testing.T, r *gin.Engine, method, target, keyHeader string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytesReader(body))
	if keyHeader != "" {
		req.Header.Set(EncKeyHeader, keyHeader)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func bytesReader(b []byte) io.Reader {
	if b == nil {
		return nil
	}
	return strings.NewReader(string(b))
}

func TestDecryptRequestOk(t *testing.T) {
	setupKeys(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()

	params := `{"acct":"admin","pwd":"p@ss123"}`
	var gotBody string
	var gotContentType string
	handlerRun := false
	r.POST("/t", wrapHandler(DecryptRequest()), wrapHandler(func(ctx *context.Context) {
		handlerRun = true
		b, _ := io.ReadAll(ctx.Request.Body)
		gotBody = string(b)
		gotContentType = ctx.Request.Header.Get("Content-Type")
		ctx.JsonSuccess(nil)
	}))

	keyHeader, body := encRequest(t, testKeyMatrix, []byte(params))
	w := doRequest(t, r, http.MethodPost, "/t", keyHeader, body)

	if !handlerRun {
		t.Fatalf("handler 未被执行")
	}
	if gotBody != params {
		t.Fatalf("明文不符: %s", gotBody)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Fatalf("Content-Type 未重写: %s", gotContentType)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("状态码: %d, body: %s", w.Code, w.Body.String())
	}
}

func TestDecryptRequestMissingHeader(t *testing.T) {
	setupKeys(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()

	handlerRun := false
	r.POST("/t", wrapHandler(DecryptRequest()), wrapHandler(func(ctx *context.Context) {
		handlerRun = true
	}))

	// 缺 X-Enc-Key 头：拒绝且 handler 不执行
	w := doRequest(t, r, http.MethodPost, "/t", "", []byte(`{}`))
	if handlerRun {
		t.Fatalf("缺头时 handler 不应执行")
	}
	if !strings.Contains(w.Body.String(), "加密密钥头") {
		t.Fatalf("错误提示不符: %s", w.Body.String())
	}
}

func TestDecryptRequestBadPayload(t *testing.T) {
	setupKeys(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/t", wrapHandler(DecryptRequest()), wrapHandler(func(ctx *context.Context) {}))

	keyHeader, _ := encRequest(t, testKeyMatrix, []byte(`{"a":1}`))

	// 明文 JSON 直传（非信封）：解密失败
	w := doRequest(t, r, http.MethodPost, "/t", keyHeader, []byte(`{"acct":"admin"}`))
	if !strings.Contains(w.Body.String(), "解密失败") && !strings.Contains(w.Body.String(), "格式错误") {
		t.Fatalf("明文直传应被拒: %s", w.Body.String())
	}

	// 密钥材料长度不对
	badKey := make([]byte, 32)
	keyHeader2 := cryptokit.BytesEncode(mustRSAEncrypt(t, testPubPem, []byte(cryptokit.BytesEncode(badKey))))
	w2 := doRequest(t, r, http.MethodPost, "/t", keyHeader2, []byte(`{}`))
	if !strings.Contains(w2.Body.String(), "解密失败") {
		t.Fatalf("密钥长度不对应被拒: %s", w2.Body.String())
	}
}

func TestDecryptRequestRawKeyMaterial(t *testing.T) {
	setupKeys(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// WebCrypto 形态：RSA 直传裸 64B 密钥材料，同样应解密成功
	var gotBody string
	r.POST("/t", wrapHandler(DecryptRequest()), wrapHandler(func(ctx *context.Context) {
		b, _ := io.ReadAll(ctx.Request.Body)
		gotBody = string(b)
	}))
	keyHeader := cryptokit.BytesEncode(mustRSAEncrypt(t, testPubPem, testKeyMatrix))
	enc, err := cryptokit.AuthAESEncrypt(testKeyMatrix, []byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(payloadEnvelope{Body: cryptokit.BytesEncode(enc)})
	w := doRequest(t, r, http.MethodPost, "/t", keyHeader, body)

	if w.Code != http.StatusOK || gotBody != `{"a":1}` {
		t.Fatalf("裸密钥材料应解密成功: code=%d body=%s resp=%s", w.Code, gotBody, w.Body.String())
	}
}

func TestDecryptRequestTamperedMAC(t *testing.T) {
	setupKeys(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/t", wrapHandler(DecryptRequest()), wrapHandler(func(ctx *context.Context) {}))

	keyHeader, body := encRequest(t, testKeyMatrix, []byte(`{"a":1}`))
	// 解码信封后翻转密文中间一个字节再编码，确保走到 MAC 校验
	var env payloadEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	raw := cryptokit.BytesDecode(env.Body)
	raw[len(raw)/2] ^= 0x01
	tampered, err := json.Marshal(payloadEnvelope{Body: cryptokit.BytesEncode(raw)})
	if err != nil {
		t.Fatal(err)
	}
	w := doRequest(t, r, http.MethodPost, "/t", keyHeader, tampered)
	if !strings.Contains(w.Body.String(), "解密失败") {
		t.Fatalf("篡改报文应被拒: %s", w.Body.String())
	}
}

func TestEncryptResponseOk(t *testing.T) {
	setupKeys(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/t", wrapHandler(EncryptResponse()), wrapHandler(func(ctx *context.Context) {
		ctx.JsonSuccess(map[string]any{"uid": 1})
	}))

	keyHeader, _ := encRequest(t, testKeyMatrix, []byte(`{}`))
	w := doRequest(t, r, http.MethodPost, "/t", keyHeader, []byte(`{}`))

	if w.Header().Get(EncPayloadHeader) != "1" {
		t.Fatalf("缺少响应加密标记头")
	}
	plain := decResponseBody(t, testKeyMatrix, w.Body.Bytes())
	if !strings.Contains(string(plain), `"result":0`) || !strings.Contains(string(plain), `"uid":1`) {
		t.Fatalf("解密后的响应不符: %s", plain)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("状态码: %d", w.Code)
	}
}

func TestEncryptResponseErrorStatusPreserved(t *testing.T) {
	setupKeys(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/t", wrapHandler(EncryptResponse()), wrapHandler(func(ctx *context.Context) {
		ctx.JsonErrorCode(3001, "参数缺失")
	}))

	keyHeader, _ := encRequest(t, testKeyMatrix, []byte(`{}`))
	w := doRequest(t, r, http.MethodPost, "/t", keyHeader, []byte(`{}`))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("业务码 3001 应映射 500，实际 %d", w.Code)
	}
	plain := decResponseBody(t, testKeyMatrix, w.Body.Bytes())
	if !strings.Contains(string(plain), `"result":3001`) || !strings.Contains(string(plain), "参数缺失") {
		t.Fatalf("解密后的响应不符: %s", plain)
	}
}

func TestEncryptResponseMissingHeader(t *testing.T) {
	setupKeys(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handlerRun := false
	r.POST("/t", wrapHandler(EncryptResponse()), wrapHandler(func(ctx *context.Context) {
		handlerRun = true
	}))

	w := doRequest(t, r, http.MethodPost, "/t", "", []byte(`{}`))
	if handlerRun {
		t.Fatalf("缺头时 handler 不应执行")
	}
	if !strings.Contains(w.Body.String(), "加密密钥头") {
		t.Fatalf("错误提示不符: %s", w.Body.String())
	}
}

func TestEncryptResponsePanicFallsBackToPlain(t *testing.T) {
	setupKeys(t)
	gin.SetMode(gin.TestMode)
	// 挂 Recover 模拟真实全局链路：panic 应得到明文错误信封，且无加密标记头
	r := gin.New()
	r.Use(wrapHandler(Recover()))
	r.POST("/t", wrapHandler(EncryptResponse()), wrapHandler(func(ctx *context.Context) {
		panic("boom")
	}))

	keyHeader, _ := encRequest(t, testKeyMatrix, []byte(`{}`))
	w := doRequest(t, r, http.MethodPost, "/t", keyHeader, []byte(`{}`))

	if w.Header().Get(EncPayloadHeader) != "" {
		t.Fatalf("panic 响应不应带加密标记头")
	}
	if !strings.Contains(w.Body.String(), "boom") {
		t.Fatalf("panic 应走 Recover 明文错误信封: %s", w.Body.String())
	}
}

func TestBothMiddlewaresEcho(t *testing.T) {
	setupKeys(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 同挂两个中间件：同一把密钥，请求解密 + 响应加密
	r.POST("/t", wrapHandler(DecryptRequest()), wrapHandler(EncryptResponse()),
		wrapHandler(func(ctx *context.Context) {
			b, err := io.ReadAll(ctx.Request.Body)
			if err != nil {
				panic(err)
			}
			var p map[string]any
			if err := json.Unmarshal(b, &p); err != nil {
				panic(err)
			}
			ctx.JsonSuccess(p)
		}))

	params := `{"acct":"admin","pwd":"p@ss123"}`
	keyHeader, body := encRequest(t, testKeyMatrix, []byte(params))
	w := doRequest(t, r, http.MethodPost, "/t", keyHeader, body)

	if w.Header().Get(EncPayloadHeader) != "1" {
		t.Fatalf("缺少响应加密标记头")
	}
	plain := decResponseBody(t, testKeyMatrix, w.Body.Bytes())
	if !strings.Contains(string(plain), "admin") || !strings.Contains(string(plain), `"result":0`) {
		t.Fatalf("双向加解密回环不符: %s", plain)
	}
}
