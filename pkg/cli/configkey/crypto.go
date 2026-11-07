package configkey

// CryptoRsaPrivateKey 报文加密 RSA 私钥（PEM，PKCS#8）。
// 留空则启动后首次使用时自动生成临时密钥（前端每请求现取公钥，重启换钥不影响使用）；
// 多实例部署必须配置统一私钥，字符串支持 ${ENV} 环境变量占位。
const CryptoRsaPrivateKey = "crypto.rsaPrivateKey"
