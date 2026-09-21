package configkey

// ragflow.* RAGFlow 知识库检索配置

// RagflowBaseUrl RAGFlow 服务地址（如 http://ragflow-host:9380）。
// 留空或 ragflow.datasetIds 未配置时不注册知识库检索工具。
const RagflowBaseUrl = "ragflow.baseUrl"

// RagflowApiKey RAGFlow API Key（Bearer 认证，RAGFlow 网页端生成）
const RagflowApiKey = "ragflow.apiKey"

// RagflowDatasetIds 要检索的知识库（dataset）ID 列表，英文逗号分隔
const RagflowDatasetIds = "ragflow.datasetIds"

// RagflowKnnTopK 参与向量相似度计算的候选 chunk 数量（默认 1024）
const RagflowKnnTopK = "ragflow.knnTopK"

// RagflowSimilarityThreshold 相似度阈值：低于该分数的 chunk 不返回（默认 0.2）
const RagflowSimilarityThreshold = "ragflow.similarityThreshold"

// RagflowVectorSimilarityWeight 向量相似度权重（0~1），剩余权重给关键词相似度（默认 0.3）
const RagflowVectorSimilarityWeight = "ragflow.vectorSimilarityWeight"

// RagflowPageSize 每次检索返回的 chunk 数量（默认 5）
const RagflowPageSize = "ragflow.pageSize"

// RagflowKeyword 是否启用关键词匹配（默认 false）
const RagflowKeyword = "ragflow.keyword"

// RagflowTimeoutSeconds 检索请求超时秒数（默认 30）
const RagflowTimeoutSeconds = "ragflow.timeoutSeconds"
