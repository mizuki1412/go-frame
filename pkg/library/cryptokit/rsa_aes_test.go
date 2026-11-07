package cryptokit

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"testing"
)

// genPair 测试辅助：生成 2048 位密钥对
func genPair(t *testing.T) (string, string) {
	t.Helper()
	priv, pub, err := GenerateRSAKeyPair(2048)
	if err != nil {
		t.Fatalf("GenerateRSAKeyPair: %v", err)
	}
	return priv, pub
}

// randBytes 测试辅助：crypto/rand 随机字节
func randBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGenerateRSAKeyPair(t *testing.T) {
	priv, pub := genPair(t)
	if !bytes.Contains([]byte(priv), []byte("PRIVATE KEY")) {
		t.Fatalf("私钥 PEM 格式不对: %s", priv)
	}
	if !bytes.Contains([]byte(pub), []byte("PUBLIC KEY")) {
		t.Fatalf("公钥 PEM 格式不对: %s", pub)
	}
	// 推导公钥应与生成的一致
	derived, err := RSAPublicFromPrivate(priv)
	if err != nil {
		t.Fatalf("RSAPublicFromPrivate: %v", err)
	}
	if derived != pub {
		t.Fatalf("推导公钥与生成公钥不一致")
	}
}

func TestRSAEncryptDecryptPKCS1v15(t *testing.T) {
	priv, pub := genPair(t)
	pt := randBytes(t, 64)
	ct, err := RSAEncrypt(pub, pt)
	if err != nil {
		t.Fatalf("RSAEncrypt: %v", err)
	}
	got, err := RSADecrypt(priv, ct)
	if err != nil {
		t.Fatalf("RSADecrypt: %v", err)
	}
	if !bytes.Equal(pt, got) {
		t.Fatalf("PKCS1v15 往返不一致")
	}
}

func TestRSADecryptOAEPFallback(t *testing.T) {
	privPem, pubPem := genPair(t)
	// 用标准库直接做 OAEP-SHA256 加密，验证 RSADecrypt 的 OAEP 优先路径
	pub, err := parsePublicKey(pubPem)
	if err != nil {
		t.Fatal(err)
	}
	pt := randBytes(t, 64)
	ct, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, pt, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := RSADecrypt(privPem, ct)
	if err != nil {
		t.Fatalf("RSADecrypt(OAEP): %v", err)
	}
	if !bytes.Equal(pt, got) {
		t.Fatalf("OAEP 往返不一致")
	}
}

func TestRSADecryptWrongKey(t *testing.T) {
	_, pub := genPair(t)
	priv2, _ := genPair(t)
	ct, err := RSAEncrypt(pub, randBytes(t, 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RSADecrypt(priv2, ct); err == nil {
		t.Fatalf("错误私钥解密应失败")
	}
}

func TestRSAParseErrors(t *testing.T) {
	if _, err := RSAEncrypt("not a pem", []byte("x")); err == nil {
		t.Fatalf("非法公钥应报错")
	}
	if _, err := RSADecrypt("not a pem", []byte("x")); err == nil {
		t.Fatalf("非法私钥应报错")
	}
}

func TestAuthAESRoundTrip(t *testing.T) {
	key := randBytes(t, AuthAESKeyLen)
	for _, size := range []int{0, 1, 15, 16, 17, 100, 4096} {
		pt := randBytes(t, size)
		enc, err := AuthAESEncrypt(key, pt)
		if err != nil {
			t.Fatalf("AuthAESEncrypt(%d): %v", size, err)
		}
		if len(enc) < 16+32 {
			t.Fatalf("密文长度异常")
		}
		got, err := AuthAESDecrypt(key, enc)
		if err != nil {
			t.Fatalf("AuthAESDecrypt(%d): %v", size, err)
		}
		if !bytes.Equal(pt, got) {
			t.Fatalf("往返不一致（size=%d）", size)
		}
	}
}

func TestAuthAESTamperRejected(t *testing.T) {
	key := randBytes(t, AuthAESKeyLen)
	enc, err := AuthAESEncrypt(key, []byte(`{"acct":"admin","pwd":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	// 篡改密文首块任意字节，MAC 校验必须失败
	enc[20] ^= 0x01
	if _, err := AuthAESDecrypt(key, enc); err == nil {
		t.Fatalf("篡改密文应校验失败")
	}
	// 篡改 MAC 也应失败
	enc2, _ := AuthAESEncrypt(key, []byte("hello"))
	enc2[len(enc2)-1] ^= 0x01
	if _, err := AuthAESDecrypt(key, enc2); err == nil {
		t.Fatalf("篡改 MAC 应校验失败")
	}
}

func TestAuthAESKeyLenInvalid(t *testing.T) {
	for _, n := range []int{0, 16, 32, 63, 65} {
		if _, err := AuthAESEncrypt(randBytes(t, n), []byte("x")); err == nil {
			t.Fatalf("key 长度 %d 应报错", n)
		}
	}
	if _, err := AuthAESDecrypt(randBytes(t, 32), make([]byte, 64)); err == nil {
		t.Fatalf("key 长度 32 应报错")
	}
}
