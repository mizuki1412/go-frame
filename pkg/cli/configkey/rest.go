package configkey

// RestServerPort rest server相关配置
const RestServerPort = "rest.port"

// RestServerBase base path
const RestServerBase = "rest.base"

// RestRequestBodySize 请求体大小上限，单位MB；0 表示不限制
const RestRequestBodySize = "rest.requestBodySize"

// RestLogRequestBody 是否打印请求参数（info 级，密码等敏感字段自动掩码）；默认 true
const RestLogRequestBody = "rest.logRequestBody"

// RestPPROF 是否开启rest server 的pprof接口： /debug/pprof
const RestPPROF = "rest.pprof"

// RestReadTimeout 读完整请求（含 body）的最大时长，单位秒；0 表示不限制
const RestReadTimeout = "rest.readTimeout"

// RestReadHeaderTimeout 读请求头的最大时长，单位秒（防 slowloris 慢请求头攻击）；0 表示不限制
const RestReadHeaderTimeout = "rest.readHeaderTimeout"

// RestWriteTimeout 从请求头读完到响应写完的最大时长，单位秒；0 表示不限制。
// 注意：SSE 等长连接场景（ssehelper）必须保持 0，否则事件流会被写超时截断
const RestWriteTimeout = "rest.writeTimeout"

// RestIdleTimeout keep-alive 空闲连接回收时长，单位秒；0 表示不限制
const RestIdleTimeout = "rest.idleTimeout"

// RestTrustedProxies 受信代理 CIDR 列表，逗号分隔；空则不信任任何代理头。
// 仅在反向代理后部署且需要取真实客户端 IP 时配置（如 "10.0.0.0/8"）
const RestTrustedProxies = "rest.trustedProxies"
