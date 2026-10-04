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

// MineruRepair 是否启用参数表 OCR 串扰纠偏：解析完成后用 PDF 文本层（pdfium）交叉修复
// 产物 markdown 中参数表的串扰字段并产出 params.json；仅对 PDF 源生效（默认开启）
const MineruRepair = "mineru.repair"

// MineruRepairLLM 是否启用纠偏残留的 LLM 仲裁：程序化纠偏修不了的串扰参数（名称串扰
// 阻断行锚定、交错型串扰），带 PDF 原文片段交模型逐字对齐，输出经程序化验证（逐字存在
// 于 PDF 文本层）后采纳；复用 llm.* 配置，llm.baseUrl 未配置时自动跳过（默认开启）
const MineruRepairLLM = "mineru.repairLLM"

// MineruImageParse 是否启用 Office 产物图片二次解析合并：Office 文档原生解析只提取内嵌
// 图片不做内容识别，产物 markdown 中每处图片引用将作为图片再次交 MinerU 解析，解析出的
// markdown 合并回引用位置（结果留存于产物目录 images-parsed/）；仅对 Office 源生效（默认开启）
const MineruImageParse = "mineru.imageParse"
