package configkey

// mineru.* MinerU 文档解析服务配置（V1 API，业务实现在主项目 mod/）

// MineruBaseUrl MinerU 服务地址（如 http://127.0.0.1:8000）。
// 留空则 mod 批量解析命令不可用
const MineruBaseUrl = "mineru.baseUrl"

// MineruApiKey MinerU API Key（Bearer 认证，--api-key 启动鉴权时填写）；本地匿名部署留空
const MineruApiKey = "mineru.apiKey"

// MineruTier 解析档位：flash/basic/standard/advanced（默认 basic；
// CPU 自建镜像只烘焙 basic 档模型，standard 会被服务端明确拒绝）
const MineruTier = "mineru.tier"

// MineruPollTimeoutSeconds 单文件解析轮询预算秒数：任务 queued/running 的最长等待，
// 超时后本次放弃（服务端任务可能仍在跑，凭 job_id 可续查）；0 不限制（默认 1800）
const MineruPollTimeoutSeconds = "mineru.pollTimeoutSeconds"
