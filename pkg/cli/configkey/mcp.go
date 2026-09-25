package configkey

// mcp.* MCP 服务器配置

// McpServers MCP 服务器配置，map 键为服务器名（注册的工具名会加 "<服务器名>__" 前缀）。
// 每个服务器支持字段：transport（stdio/sse/http，默认 stdio）、command/args/env（stdio）、
// url/headers（sse/http）、tools（只注册指定工具，留空注册全部）、
// timeoutSeconds（单次工具调用超时秒数，默认 60，0 不限制）。
// 结构化 map 无法用 cobra flag 表达，故不注册 flag，仅由 config.yml 提供
// （未绑定 flag 的键：config.yml 值直接可见，业务侧 GetStringMap 以空 map 兜底）。
const McpServers = "mcp.servers"
