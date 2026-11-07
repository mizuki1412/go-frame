package cryptokit

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"github.com/example/go-frame/pkg/class/exception"
	"golang.org/x/crypto/bcrypt"
)

func MD5(o string) string {
	m := md5.Sum([]byte(o))
	return hex.EncodeToString(m[:])
}

// HashPwd 使用 bcrypt 加密密码。密码类存储必须使用本函数（AGENTS.md：新代码禁用 MD5）。
func HashPwd(pwd string) string {
	b, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	if err != nil {
		panic(exception.New("密码加密失败: " + err.Error()))
	}
	return string(b)
}

// CheckPwd 校验明文密码与存储哈希是否匹配。
// 兼容两种格式：bcrypt（$2a$/$2b$ 前缀）直接比对；否则按存量 MD5 比对，
// 调用方应在 MD5 命中后择机用 HashPwd 重存升级（参见 NeedUpgrade）。
func CheckPwd(pwd, stored string) bool {
	if strings.HasPrefix(stored, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(pwd)) == nil
	}
	return MD5(pwd) == stored
}

// NeedUpgrade 存量哈希是否仍是 MD5、需要升级为 bcrypt。
func NeedUpgrade(stored string) bool {
	return !strings.HasPrefix(stored, "$2")
}

// PwdError 密码强度规则：字符数至少 8 位（按字符而非字节计，多字节字符不占便宜），
// 且大写字母、小写字母、数字、特殊符号四类字符全含。返回空串表示通过。
// 非 ASCII 字符（含中文）一律计入特殊符号，与前端 [^A-Za-z0-9] 口径一致。
func PwdError(p string) string {
	if p == "" {
		return "请输入密码"
	}
	if utf8.RuneCountInString(p) < 8 {
		return "密码至少 8 位"
	}
	var hasLower, hasUpper, hasDigit, hasSpecial bool
	for _, c := range p {
		switch {
		case c >= 'a' && c <= 'z':
			hasLower = true
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= '0' && c <= '9':
			hasDigit = true
		default:
			hasSpecial = true
		}
	}
	if !hasLower {
		return "密码需包含小写字母"
	}
	if !hasUpper {
		return "密码需包含大写字母"
	}
	if !hasDigit {
		return "密码需包含数字"
	}
	if !hasSpecial {
		return "密码需包含特殊字符"
	}
	return ""
}

// URLEncode 这里是base64的，中文url用url.QueryEscape
func URLEncode(s string) string {
	// 不用 RawURLEncoding：其会去掉 == 填充
	return base64.URLEncoding.EncodeToString([]byte(s))
}

func BytesEncode(bytes []byte) string {
	return base64.StdEncoding.EncodeToString(bytes)
}

func BytesDecode(str string) []byte {
	bytes, _ := base64.StdEncoding.DecodeString(str)
	return bytes
}

func HmacSha256(message []byte, secret string) string {
	key := []byte(secret)
	h := hmac.New(sha256.New, key)
	h.Write(message)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
