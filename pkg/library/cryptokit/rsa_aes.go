package cryptokit

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

// AuthAESKeyLen 认证加密密钥材料总长：AES-256 密钥(32B) + HMAC-SHA256 密钥(32B)。
// 前端随机生成后经 RSA 公钥加密传输，见 restkit 报文加密协议。
const AuthAESKeyLen = 64

// GenerateRSAKeyPair 生成 RSA 密钥对，返回 PEM 编码的私钥（PKCS#8）与公钥（PKIX/SPKI）。
func GenerateRSAKeyPair(bits int) (privatePem, publicPem string, err error) {
	priv, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return "", "", err
	}
	privDer, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", err
	}
	pubDer, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return "", "", err
	}
	privatePem = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDer}))
	publicPem = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDer}))
	return privatePem, publicPem, nil
}

// RSAPublicFromPrivate 由 PEM 私钥推导 PEM 公钥（PKIX/SPKI）。
func RSAPublicFromPrivate(privatePem string) (string, error) {
	priv, err := parsePrivateKey(privatePem)
	if err != nil {
		return "", err
	}
	pubDer, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDer})), nil
}

// RSAEncrypt 用 PEM 公钥做 RSA-PKCS1v15 加密（与 jsencrypt 输出兼容，用于测试/工具）。
func RSAEncrypt(publicPem string, plaintext []byte) ([]byte, error) {
	pub, err := parsePublicKey(publicPem)
	if err != nil {
		return nil, err
	}
	return rsa.EncryptPKCS1v15(rand.Reader, pub, plaintext)
}

// RSADecrypt 用 PEM 私钥解密：先试 OAEP-SHA256（WebCrypto 仅支持 OAEP），失败回退
// PKCS1v15（jsencrypt 仅支持该填充），前端两种实现均可对接。
func RSADecrypt(privatePem string, ciphertext []byte) ([]byte, error) {
	priv, err := parsePrivateKey(privatePem)
	if err != nil {
		return nil, err
	}
	if pt, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, priv, ciphertext, nil); err == nil {
		return pt, nil
	}
	return rsa.DecryptPKCS1v15(rand.Reader, priv, ciphertext)
}

// AuthAESEncrypt 认证加密：AES-256-CBC + HMAC-SHA256（encrypt-then-MAC）。
// key 须为 AuthAESKeyLen(64B)：前 32B 作 AES 密钥、后 32B 作 HMAC 密钥；
// 输出 IV(16B) || 密文(PKCS7 填充) || MAC(32B)，MAC 覆盖 IV||密文。
// 不用 GCM：前端纯 JS 生态在非安全上下文（明文 HTTP）拿不到 WebCrypto，
// CBC+HMAC 用 crypto-js 即可实现且任何环境可用。
func AuthAESEncrypt(key, plaintext []byte) ([]byte, error) {
	aesKey, macKey, err := splitAuthAESKey(key)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	out := make([]byte, 0, len(iv)+len(ct)+sha256.Size)
	out = append(out, iv...)
	out = append(out, ct...)
	mac := hmac.New(sha256.New, macKey)
	mac.Write(out) // MAC 覆盖 IV||密文
	return mac.Sum(out), nil
}

// AuthAESDecrypt 认证解密：先常量时间校验 MAC 再解密，防密文篡改（padding oracle 面）。
// 输入格式见 AuthAESEncrypt。
func AuthAESDecrypt(key, data []byte) ([]byte, error) {
	aesKey, macKey, err := splitAuthAESKey(key)
	if err != nil {
		return nil, err
	}
	if len(data) < aes.BlockSize+sha256.Size {
		return nil, errors.New("加密报文长度不足")
	}
	body, mac := data[:len(data)-sha256.Size], data[len(data)-sha256.Size:]
	check := hmac.New(sha256.New, macKey)
	check.Write(body)
	if !hmac.Equal(mac, check.Sum(nil)) {
		return nil, errors.New("报文完整性校验失败")
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}
	iv, ct := body[:aes.BlockSize], body[aes.BlockSize:]
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, errors.New("加密报文长度非块对齐")
	}
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(pt, ct)
	return pkcs7Unpad(pt, aes.BlockSize)
}

// splitAuthAESKey 拆分 64B 密钥材料为 AES 密钥与 HMAC 密钥。
func splitAuthAESKey(key []byte) (aesKey, macKey []byte, err error) {
	if len(key) != AuthAESKeyLen {
		return nil, nil, fmt.Errorf("密钥材料长度须为 %d 字节，实际 %d", AuthAESKeyLen, len(key))
	}
	return key[:32], key[32:], nil
}

// pkcs7Pad 按 PKCS#7 填充至 blockSize 整数倍（空明文补一个整块）。
func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	padded := make([]byte, 0, len(data)+pad)
	padded = append(padded, data...)
	return append(padded, bytes.Repeat([]byte{byte(pad)}, pad)...)
}

// pkcs7Unpad 去除 PKCS#7 填充，校验填充值合法性。
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("填充数据长度非法")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > blockSize || pad > len(data) {
		return nil, errors.New("填充值非法")
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, errors.New("填充值非法")
		}
	}
	return data[:len(data)-pad], nil
}

// parsePrivateKey 解析 PKCS#8 PEM 私钥并断言为 RSA。
func parsePrivateKey(privatePem string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(privatePem))
	if block == nil {
		return nil, errors.New("私钥 PEM 解析失败")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("私钥格式须为 PKCS#8")
	}
	priv, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("私钥须为 RSA")
	}
	return priv, nil
}

// parsePublicKey 解析 PKIX/SPKI PEM 公钥并断言为 RSA。
func parsePublicKey(publicPem string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(publicPem))
	if block == nil {
		return nil, errors.New("公钥 PEM 解析失败")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("公钥格式须为 PKIX/SPKI")
	}
	pub, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("公钥须为 RSA")
	}
	return pub, nil
}
