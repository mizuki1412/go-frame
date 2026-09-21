package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/service/configkit"
	"github.com/example/go-frame/pkg/service/logkit"
)

// ragSearchToolName 知识库检索工具名
const ragSearchToolName = "rag_search"

// ragSearchInput rag_search 工具入参（由模型生成的 JSON 反序列化而来）
type ragSearchInput struct {
	Question string `json:"question" jsonschema:"description=The question or keywords to search in the knowledge base. A complete natural-language question works best."`
}

// ragSearchOutput rag_search 工具出参：整理后的检索结果文本
type ragSearchOutput struct {
	Result string `json:"result"`
}

// ragflowRetrievalResp RAGFlow POST /api/v1/retrieval 响应（只解析用到的字段）
type ragflowRetrievalResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Total  int            `json:"total"`
		Chunks []ragflowChunk `json:"chunks"`
	} `json:"data"`
}

// ragflowChunk 单个知识块。不同 RAGFlow 版本的文档名字段不同
// （document_name / document_keyword），两个都解析，取非空者。
type ragflowChunk struct {
	Content         string  `json:"content"`
	DocumentName    string  `json:"document_name"`
	DocumentKeyword string  `json:"document_keyword"`
	Similarity      float64 `json:"similarity"`
}

// newRagSearchTool 创建 RAGFlow 知识库检索工具；ragflow.baseUrl 或
// ragflow.datasetIds 未配置时返回 (nil, nil)，表示不启用该工具。
func NewRagSearchTool() (tool.InvokableTool, error) {
	if configkit.GetString(configkey.RagflowBaseUrl) == "" ||
		configkit.GetString(configkey.RagflowDatasetIds) == "" {
		return nil, nil
	}
	t, err := toolutils.InferTool(ragSearchToolName,
		"Search the RAGFlow knowledge base for domain documents (internal docs, "+
			"product manuals, etc.). Use it when the user's question likely needs "+
			"knowledge-base content that neither your own knowledge nor the local "+
			"workspace files can answer.",
		ragSearch)
	if err != nil {
		return nil, exception.New("创建 rag_search 工具失败: " + err.Error())
	}
	return t, nil
}

// ragSearch 执行检索：HTTP 调 RAGFlow 的 POST /api/v1/retrieval（Bearer 认证），
// 返回按相似度排序的知识块文本。检索参数（阈值/权重/条数等）全部来自配置。
func ragSearch(ctx context.Context, in ragSearchInput) (ragSearchOutput, error) {
	if strings.TrimSpace(in.Question) == "" {
		return ragSearchOutput{}, exception.New("question 不能为空")
	}

	reqBody, err := json.Marshal(map[string]any{
		"question":                 in.Question,
		"dataset_ids":              ragflowDatasetIds(),
		"page":                     1,
		"page_size":                configkit.GetInt(configkey.RagflowPageSize, 5),
		"similarity_threshold":     getFloatConfig(configkey.RagflowSimilarityThreshold, 0.2),
		"vector_similarity_weight": getFloatConfig(configkey.RagflowVectorSimilarityWeight, 0.3),
		"knn_top_k":                configkit.GetInt(configkey.RagflowKnnTopK, 1024),
		"keyword":                  configkit.GetBool(configkey.RagflowKeyword, false),
	})
	if err != nil {
		return ragSearchOutput{}, exception.New("构造检索请求失败: " + err.Error())
	}

	url := strings.TrimSuffix(configkit.GetString(configkey.RagflowBaseUrl), "/") + "/api/v1/retrieval"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return ragSearchOutput{}, exception.New("构造检索请求失败: " + err.Error())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+configkit.GetString(configkey.RagflowApiKey))

	timeout := time.Duration(configkit.GetInt(configkey.RagflowTimeoutSeconds, 30)) * time.Second
	httpResp, err := (&http.Client{Timeout: timeout}).Do(httpReq)
	if err != nil {
		return ragSearchOutput{}, exception.New("请求 RAGFlow 失败: " + err.Error())
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return ragSearchOutput{}, exception.New(fmt.Sprintf("RAGFlow 返回状态 %d: %s", httpResp.StatusCode, string(body)))
	}

	var resp ragflowRetrievalResp
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return ragSearchOutput{}, exception.New("解析 RAGFlow 响应失败: " + err.Error())
	}
	if resp.Code != 0 {
		return ragSearchOutput{}, exception.New("RAGFlow 检索失败: " + resp.Message)
	}

	logkit.Info("[rag_search]", "question", in.Question, "chunks", len(resp.Data.Chunks))
	return ragSearchOutput{Result: formatChunks(resp.Data.Chunks)}, nil
}

// ragflowDatasetIds 解析配置的知识库 ID 列表（英文逗号分隔，去空白）
func ragflowDatasetIds() []string {
	raw := strings.Split(configkit.GetString(configkey.RagflowDatasetIds), ",")
	ids := make([]string, 0, len(raw))
	for _, id := range raw {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// formatChunks 把检索到的知识块列表整理成模型可读的文本
func formatChunks(chunks []ragflowChunk) string {
	if len(chunks) == 0 {
		return "知识库中未检索到相关内容。"
	}
	var sb strings.Builder
	for i, c := range chunks {
		name := c.DocumentName
		if name == "" {
			name = c.DocumentKeyword
		}
		fmt.Fprintf(&sb, "[%d] 来源: %s | 相似度: %.2f\n%s\n\n", i+1, name, c.Similarity, c.Content)
	}
	return sb.String()
}

// getFloatConfig 读取浮点配置（configkit 无 GetFloat，按字符串读出后解析）
func getFloatConfig(key string, def float64) float64 {
	if s := configkit.GetString(key); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v
		}
	}
	return def
}
