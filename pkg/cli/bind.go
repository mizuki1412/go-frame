package cli

import (
	"errors"
	"os"
	"regexp"

	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// envPlaceholderRe 配置值中的 ${ENV_NAME} 占位符（ENV_NAME 为合法环境变量名）
var envPlaceholderRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// 这里将在 run 之后执行
func loadConfig() {
	explicit := viper.GetString("config") != ""
	if explicit {
		viper.SetConfigFile(viper.GetString("config"))
	} else {
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
	}
	err := viper.ReadInConfig()
	if err == nil {
		expandEnvPlaceholders()
		return
	}
	// P1 修复：原实现吞掉全部错误。搜索模式下「未找到配置文件」属正常（纯 flag/环境变量启动）；
	// 但显式指定路径（-c）缺失、或 YAML 解析失败必须报错——
	// 否则服务会带着默认配置静默起跑（叠加空 JWT 密钥等问题，隐患极大）。
	var cfgNotFound viper.ConfigFileNotFoundError
	if !explicit && errors.As(err, &cfgNotFound) {
		return
	}
	panic(exception.New("配置文件加载失败: " + err.Error()))
}

// expandEnvPlaceholders 将配置中形如 ${ENV_NAME} 的字符串值展开为环境变量值。
// 环境变量未设置时展开为空串（调用方应自行校验敏感键）。
func expandEnvPlaceholders() {
	for _, key := range viper.AllKeys() {
		raw, ok := viper.Get(key).(string)
		if !ok || !envPlaceholderRe.MatchString(raw) {
			continue
		}
		viper.Set(key, envPlaceholderRe.ReplaceAllStringFunc(raw, func(m string) string {
			// m 形如 ${NAME}，去掉 ${ 与 } 取变量名
			return os.Getenv(m[2 : len(m)-1])
		}))
	}
}

func bindDefaultFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringP("config", "c", "", "配置文件全路径")
	cmd.PersistentFlags().String(configkey.ProjectDir, ".", "项目目录")
	cmd.PersistentFlags().String(configkey.ProjectName, "app", "项目名称")
	cmd.PersistentFlags().String(configkey.ProjectSubDir4PublicDownload, "", "项目目录中用于公共下载的开放目录（一层），逗号分隔，.表示所有")
	cmd.PersistentFlags().String(configkey.ProjectSubDir4PrivateDownload, "", "项目目录中用于私有下载的开放目录（一层），逗号分隔，.表示所有")
	cmd.PersistentFlags().Bool(configkey.ProfileDev, false, "开发模式 default:false")
	cmd.PersistentFlags().String(configkey.TimeLocation, "Asia/Shanghai", "项目中用到的时区")

	cmd.PersistentFlags().Int(configkey.CacheWrapperTTL, 1, "wrapper ttl 默认1s")

	cmd.PersistentFlags().String(configkey.RedisPrefix, "", "redis key的前缀")
	cmd.PersistentFlags().String(configkey.RedisHost, "", "redis host")
	cmd.PersistentFlags().String(configkey.RedisPort, "", "")
	cmd.PersistentFlags().String(configkey.RedisDB, "", "redis db 数据库号")
	cmd.PersistentFlags().String(configkey.RedisPwd, "", "")

	cmd.PersistentFlags().String(configkey.LogPath, "", "日志目录；空则表示在project.dir/log下；不填不开启文件日志")
	cmd.PersistentFlags().String(configkey.LogName, "main", "日志文件名，无后缀")
	cmd.PersistentFlags().Int(configkey.LogMaxRemain, 0, "最大保留天数")
	cmd.PersistentFlags().Int(configkey.LogMaxBackups, 0, "最大保留个数")
	cmd.PersistentFlags().Int(configkey.LogMaxSize, 20, "单文件最大尺寸")
	cmd.PersistentFlags().String(configkey.LogLevel, "", "日志等级 debug/info/warn/error")
	cmd.PersistentFlags().String(configkey.LogType, "text", "日志写入时的格式 text/json")

	cmd.PersistentFlags().String(configkey.RestServerBase, "", "rest base url")
	cmd.PersistentFlags().String(configkey.RestServerPort, "10000", "")
	cmd.PersistentFlags().Int(configkey.RestRequestBodySize, 32, "限制request最大，单位MB，0不限制")
	cmd.PersistentFlags().Bool(configkey.RestLogRequestBody, true, "打印请求参数(info级，密码等敏感字段掩码)")
	cmd.PersistentFlags().String(configkey.RestTrustedProxies, "", "受信代理CIDR列表，逗号分隔；空则不信任任何代理头")
	cmd.PersistentFlags().Bool(configkey.RestPPROF, false, "开启pprof, /debug/pprof")

	cmd.PersistentFlags().Int(configkey.RestReadTimeout, 60, "读完整请求(含body)超时/秒，0不限制")
	cmd.PersistentFlags().Int(configkey.RestReadHeaderTimeout, 10, "读请求头超时/秒(防slowloris慢速攻击)，0不限制")
	cmd.PersistentFlags().Int(configkey.RestWriteTimeout, 0, "响应写超时/秒；0不限制(SSE等长连接场景必须保持0)")
	cmd.PersistentFlags().Int(configkey.RestIdleTimeout, 120, "keep-alive空闲连接回收/秒，0不限制")

	cmd.PersistentFlags().Int(configkey.TokenExpire, 168, "token 会话绝对上限/小时（连续活跃也会到期，须重新登录）")
	cmd.PersistentFlags().Int(configkey.TokenIdle, 1, "token 空闲窗口/小时（滑动续期：每次鉴权通过即重置；<=0 退化为不滑动）")
	cmd.PersistentFlags().Bool(configkey.TokenMultiLogin, true, "是否允许同一账号多端同时在线；false 时新登录剔出旧会话")

	cmd.PersistentFlags().String(configkey.DBDriver, "", "postgres/mysql/mssql")
	cmd.PersistentFlags().String(configkey.DBHost, "", "")
	cmd.PersistentFlags().String(configkey.DBPort, "", "")
	cmd.PersistentFlags().String(configkey.DBName, "", "")
	cmd.PersistentFlags().String(configkey.DBUser, "", "")
	cmd.PersistentFlags().String(configkey.DBPwd, "", "")
	cmd.PersistentFlags().Int(configkey.DBMaxOpen, 25, "最大连接")
	cmd.PersistentFlags().Int(configkey.DBMaxIdle, 5, "最大空闲连接")
	cmd.PersistentFlags().Int(configkey.DBMaxLife, 10, "单位/分钟")
	cmd.PersistentFlags().String(configkey.DBSSLMode, "disable", "PG/Kingbase 的 sslmode（disable/require/verify-ca/verify-full）")

	cmd.PersistentFlags().String(configkey.OpenApiDescription, "openapi doc", "")
	cmd.PersistentFlags().String(configkey.OpenApiTitle, "openapi doc", "")
	cmd.PersistentFlags().String(configkey.OpenApiVersion, "1.0.0", "")
	cmd.PersistentFlags().String(configkey.OpenApiContactName, "", "")
	cmd.PersistentFlags().String(configkey.OpenApiContactUrl, "", "")
	cmd.PersistentFlags().String(configkey.OpenApiContactEmail, "", "")

	cmd.PersistentFlags().String(configkey.AmapKey, "", "高德key")

	cmd.PersistentFlags().String(configkey.AliRegionId, "cn-hangzhou", "ali")
	cmd.PersistentFlags().String(configkey.AliAccessKey, "", "ali")
	cmd.PersistentFlags().String(configkey.AliAccessKeySecret, "", "ali")
	cmd.PersistentFlags().String(configkey.AliSMSTemplate1, "", "ali sms 模板1")
	cmd.PersistentFlags().String(configkey.AliSMSSign1, "", "ali sms 签名1")
	cmd.PersistentFlags().String(configkey.AliSTSRoleArn, "", "ali sts")
	cmd.PersistentFlags().String(configkey.AliOSSBucketName, "", "ali oss default bucket")

	// mqtt
	cmd.PersistentFlags().String(configkey.MQTTBroker, "", "eg: tcp://xx.xx.xx")
	cmd.PersistentFlags().String(configkey.MQTTClientID, "client", "")
	cmd.PersistentFlags().String(configkey.MQTTUsername, "", "")
	cmd.PersistentFlags().String(configkey.MQTTPwd, "", "")
	cmd.PersistentFlags().Bool(configkey.MQTTInsecureSkipVerify, false, "mqtt ssl 连接跳过 TLS 证书校验（自签证书场景显式开启）")
	// netkit
	cmd.PersistentFlags().String(configkey.NetPort, "", "")

	// softether
	cmd.PersistentFlags().String(configkey.SoftEtherHost, "", "")
	cmd.PersistentFlags().String(configkey.SoftEtherPort, "", "")
	cmd.PersistentFlags().String(configkey.SoftEtherPwd, "", "")
	cmd.PersistentFlags().String(configkey.SoftEtherOpenVpnPort, "", "")

	// rustfs/aws s3
	cmd.PersistentFlags().String(configkey.FSEndpoint, "127.0.0.1:9000", "")
	cmd.PersistentFlags().String(configkey.FSAccessKey, "", "")
	cmd.PersistentFlags().String(configkey.FSSecret, "", "")

	// agent（大模型 Agent 交互 demo，实现见 agent/runtime）
	cmd.PersistentFlags().String(configkey.LLMBaseUrl, "", "大模型服务地址（OpenAI 兼容端点）")
	cmd.PersistentFlags().String(configkey.LLMApiKey, "", "大模型 API Key")
	cmd.PersistentFlags().String(configkey.LLMModel, "", "大模型名称")
	cmd.PersistentFlags().Int(configkey.LLMMaxTokens, 0, "单次生成最大 token 数，0 表示不限制")
	cmd.PersistentFlags().String(configkey.LLMApiType, "openai-chat-completions", "接口协议类型：openai-chat-completions / anthropic-messages")

	cmd.PersistentFlags().String(configkey.AgentSkillsDir, "", "skill 目录（每个子目录一个 skill，含 SKILL.md）")
	cmd.PersistentFlags().Bool(configkey.AgentStream, true, "是否开启流式输出")
	cmd.PersistentFlags().Int(configkey.AgentMaxIterations, 20, "单次运行内 model→tool 循环的最大轮数，0 表示框架默认")
	cmd.PersistentFlags().String(configkey.AgentCheckpointDir, "", "CheckPointStore 本地目录，留空不启用 checkpoint")
	cmd.PersistentFlags().String(configkey.AgentWorkspaceDir, "", "agent 工作区目录，留空取程序启动时的工作目录")
	cmd.PersistentFlags().String(configkey.AgentSessionDir, "", "会话历史持久化目录，留空不持久化会话历史")
	cmd.PersistentFlags().Int(configkey.AgentRunTimeout, 600, "单轮运行超时秒数（含模型请求与工具执行），0 不限制")
	cmd.PersistentFlags().Int(configkey.AgentSummarizationTriggerTokens, 100000, "会话历史 token 压缩触发阈值，0 不启用摘要压缩")
	cmd.PersistentFlags().Int(configkey.AgentToolResultMaxChars, 50000, "单条工具结果最大字符数，超出卸载到本地文件，0 不截断")
	cmd.PersistentFlags().Int(configkey.AgentToolResultClearTokens, 160000, "历史工具结果总 token 清理阈值，0 不清理")
	cmd.PersistentFlags().String(configkey.AgentAgentsMdFiles, "AGENTS.md", "注入模型输入的 AGENTS.md 文件列表（逗号分隔，相对工作区解析），留空不注入")

	// ragflow 知识库检索
	cmd.PersistentFlags().String(configkey.RagflowBaseUrl, "", "RAGFlow 服务地址，如 http://ragflow-host:9380")
	cmd.PersistentFlags().String(configkey.RagflowApiKey, "", "RAGFlow API Key（Bearer 认证）")
	cmd.PersistentFlags().String(configkey.RagflowDatasetIds, "", "要检索的知识库 dataset ID 列表，英文逗号分隔")
	cmd.PersistentFlags().Int(configkey.RagflowPageSize, 5, "每次检索返回的 chunk 数量")
	cmd.PersistentFlags().String(configkey.RagflowSimilarityThreshold, "0.2", "相似度阈值：低于该分数的 chunk 不返回")
	cmd.PersistentFlags().String(configkey.RagflowVectorSimilarityWeight, "0.3", "向量相似度权重（0~1），剩余权重给关键词相似度")
	cmd.PersistentFlags().Int(configkey.RagflowKnnTopK, 1024, "参与向量相似度计算的候选 chunk 数量")
	cmd.PersistentFlags().Bool(configkey.RagflowKeyword, false, "是否启用关键词匹配")
	cmd.PersistentFlags().Int(configkey.RagflowTimeoutSeconds, 30, "检索请求超时秒数")
}

func bind(cmd *cobra.Command) {
	// 所有类型flag
	err := viper.BindPFlags(cmd.Flags())
	if err != nil {
		panic(err)
	}
}
