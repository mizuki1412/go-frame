# AGENTS.md

Go scaffold for vibe-coding REST services (gin + viper + cobra + sqlx/squirrel + PostgreSQL), plus an eino-ADK agent demo. Repo docs/comments are in Chinese; match that style.

## Must read first

`doc/effective_go.md` — 编写本项目代码的规范依据（三源融合知识图谱：官方 Effective Go + 《Go专家编程》 + 《Mastering Go》，末章含本项目对照审计清单）。

## Current state (verified)

- Build, vet and tests all green (verified 2026-09-25, after the sqlkit refactor: 全链路 ctx 变体、UpsertObj、InsertBatch 分片、jsonb key 参数化、SelectEx/FromSubQuery 修复). The required verification for every change is:
  ```
  go build ./... && go vet ./... && gofmt -l . && go test ./...
  ```
  Caveat: on a Windows checkout with `core.autocrlf=true`, `gofmt -l .` lists nearly every file because files are CRLF on disk (repo stores LF). `gofmt -d` shows content-identical diffs — treat that as clean; only real formatting diffs count.
- Tests: pkg infra (`pkg/class`, `pkg/library/cmdkit`, `pkg/library/cryptokit`, `pkg/library/framekit`, `pkg/service/tokenkit`, `pkg/service/sqlkit`（免 DB 的 SQL 生成单测）, …) plus `agent/runtime/sessionstore` and `pkg/cli` (bind_agent_test); no `mod/` tests, no lint config, no CI.
- Module path `github.com/example/go-frame` and project name `go-frame` are placeholders; rename (go.mod module path + import prefixes, at minimum) before reuse.
- Go toolchain: go 1.27.0 (go.mod `go 1.27.0`).
- Business modules: `mod/user/`. Agent runtime lives outside `mod/` in `agent/` (infra-like, may not be imported by `mod/*`).

## Layout

```
pkg/                          # reusable infra, MUST NOT depend on mod/*
  class/                        nullable DB wrappers: String, Int64, Time, Decimal, ArrInt, MapString, File, etc.
  library/*kit/                 pure utility packages
  service/*kit/                 infra kits: restkit, sqlkit, configkit, logkit, tokenkit, rediskit, aikit, mqttkit, netkit, cachekit, cronkit, excelkit, pdfkit, serialkit, storagekit
  cli/                          cobra root command + viper config binding
agent/                        # eino-ADK agent runtime (see Agent runtime below)
  runtime/                      model.go (chat model factory), runner.go (Runner + middleware wiring)
  runtime/sessionstore/         会话历史持久化：每个 session ID 一个目录（history.json 消息历史 + perf.json 调用耗时明细）
  tools/                        agent 工具（ragflow.go: RAGFlow 知识库检索 rag_search）
mod/<name>/                   # business module, strict 4 layers
  mod.go                        All() []func(*router.Router) — aggregate each resource's Init
  model/                        entity structs (db/json/pk/table tags only, no HTTP tags)
  dao/<resource>dao/            embeds sqlkit.Dao[T] + CascadeOpts + query methods
  service/                      function-style (no structs), business logic + TxArea orchestration
  controller/<resource>/        index.go (routing + OpenAPI), *_controller.go (handlers)
cmd/                          # extra cobra subcommands, registered via cli.AddChildCMD(...)
                                agent.go（agent demo REPL）, mqtt.go, tcp_server.go, web_static_server.go
sql/                          # user 模块 DDL：user.mysql8.sql（MySQL 8.0+）/ user.pgsql.sql（PG 12+），两版语义一致
main.go                       # cli.RootCMD(...) → user.Init()（注册权限数据源）→ restkit.AddActions(user.All()...) → restkit.Run()
```

## User module data model (mod/user)

- `sys_user` 单列 `department`（部门决定数据范围）；角色为**多对多**：`sys_user_role` 中间表（组合主键 userid+roleid，无逻辑删除），`User.Roles []*Role` 无 db tag，仅由 `userdao` 的 `NewCascadeManyLink` 经中间表批量装配（整批 2 条 SQL）。角色为全局对象，`Role` **不再有 Department**。
- `immutable`（user/role/department）是正式列，不再塞 Extend；`Extend` 只放 `privilegeExclude`。`role id=0` 为内置超级管理员（`model.RoleIdSuperAdmin`），持有者禁止经管理员接口改绑/删除。
- **权限判定的运行时权威在 `tokenkit.Principal`**（多角色并集 − 剔除，且支持 `*` 通配）；`service.LoadPrincipal` 是唯一的 `Principal` 装配入口。`model.User.HasPrivilege` 只做精确匹配，供 model 层内部使用。
- 权限码常量集中在 `model/privilege.go`，与 `sql/*.sql` 的 `sys_privilege_constant` 种子数据一一对应（两边必须同步）。内置超级管理员角色种子 `privileges = ['*']`。
- 鉴权数据源装配用 `tokenkit.SetLoader(service.LoadPrincipal)`；新增业务模块时要为其权限码与 `sys_privilege_constant` 补种子，否则字典接口不展示但鉴权已生效。
- 角色绑定为整体替换语义（`userroledao.ReplaceForUser`：先删后插），AddUser/UpdateUser 中用户行与绑定同事务（`sqlkit.TxArea` + 用 `targetDS` 构造 dao）。
- 四张业务表均有 `createdt`/`updatedt`，服务层每次 `UpdateObj` 前显式 `UpdateDt.Set(time.Now())`（sqlkit 不自动填充）。

## Config

- Every key is a `const` in `pkg/cli/configkey/*.go`, bound as cobra flag in `pkg/cli/bind.go`, read via `configkit.GetString/GetInt/GetBool(key, default...)` — never read viper directly.
- Defaults: `:10000` for REST server; `/v3/api-docs` serves the OpenAPI JSON (no bundled UI). REST 另有 `rest.requestBodySize`（请求体上限 MB，默认 32，0 不限制，经 `middleware.MaxBody` 接线）与 `rest.logRequestBody`（请求参数 info 级日志开关，默认 true，pwd/password/passwd 自动掩码）。
- DB 另有 `db.sslMode`（PG/Kingbase sslmode：disable/require/verify-ca/verify-full，默认 disable；MySQL DSN 固定 utf8mb4，loc 取 timekit 时区）。
- Config file: `config.yaml` in working dir, override with `-c/--config`.
- Env placeholders: after `ReadInConfig`, `loadConfig` expands `${ENV_NAME}` in string config values to `os.Getenv(ENV_NAME)`; unset vars expand to empty string (2026-09-19, VERSION 20260919).
- Key families (each in its own `configkey/*.go`): rest/db/token/redis/... plus the agent stack:
  - `token.*`: expire（会话绝对上限/小时，默认 168）、idle（空闲窗口/小时，默认 1，`<=0` 退化为不滑动）、multiLogin（是否多端在线，默认 true）。token 不透明、无签名密钥，**没有 `token.secret` 这一类密钥配置**。
  - `llm.*`: baseUrl, apiKey, model, maxTokens, apiType (`openai-chat-completions` 默认 / `anthropic-messages`).
  - `agent.*`: skillsDir, stream, maxIterations, checkpointDir, workspaceDir, sessionDir — 全部留空安全降级（不启用对应能力）.
  - `ragflow.*`: baseUrl, apiKey, datasetIds（前两者 + datasetIds 配齐才注册 rag_search 工具）+ 检索参数（knnTopK/similarityThreshold/…）.

## Conventions

### Layer boundaries
```
controller: ctx.BindForm(&params) → call service → ctx.JsonSuccess(ret)
service:    business logic, TxArea, cross-dao assembly, panic(exception.New("...")) on error
dao:        SQL only — parameterized Where("col=?", v), WhereJsonbPathEq, WithRecursiveRaw. Never fmt.Sprintf.
model:      struct tags: db, json, pk, table, auto, logicDel, comment, default, validate.
```

**Forbidden**: controller→dao direct call, dao business logic, service touching `*context.Context`, model with HTTP tags, `pkg/*` importing `mod/*`.

### Naming

| Element | Rule | Example |
|---|---|---|
| Module dir | `mod/<singular_lower>/` | `mod/user/` |
| Resource dir | `dao/<resource>dao/`, `controller/<resource>/` | `dao/userdao/`, `controller/user/` |
| DAO struct | `type Dao struct { sqlkit.Dao[model.User] }` | embedded |
| DAO New | `userdao.New(opts CascadeOpts, ds ...*sqlkit.DataSource) Dao` | |
| Service | `package service`, function-style (no struct) | `func Login(username, phone, pwd string)` |
| Controller handler | `func Xxx(ctx *context.Context)` | `LoginByUsername`, `ListUsers` |
| Init | `func Init(router *router.Router)` — one per resource sub-package | in `index.go` |
| Route prefix | `/<resource>` or `/<resource>/admin` | `/user`, `/user/admin` |

### DAO cascade

```go
type CascadeOpts struct { Role bool; Department bool }
var OptsDefault = CascadeOpts{Role: true, Department: true}
// New registers cascades via dao.WithCascadeOpts(opts, func(obj *T, ctx sqlkit.CascadeCtx) { ... })
```
Batch strategy with `WithCascadeBatchLinks` to avoid N+1. No byte enums.

### sqlkit 数据访问 (pkg/service/sqlkit)

- 全链路提供 `*context.Context` 的 `Ctx` 后缀变体（`OneCtx/ListCtx/PageCtx/CountCtx/ExecCtx/QueryRawCtx/SelectOneByIdCtx/...`）；无 ctx 的老方法等价于 `context.Background()`，REST/异步场景需要超时与取消传播时用 Ctx 变体。
- `UpsertObj(dest, conflictCols...)`：PG/SQLite 走 `ON CONFLICT(...) DO UPDATE`、MySQL 走 `ON DUPLICATE KEY UPDATE`，更新列为全部可更新列减主键/自增/冲突列；`Replace()` 仅 MySQL/SQLite（其他驱动提前 panic）。`InsertBatch` 按列数自动分片（单语句参数 ≤32768，防 PG 65535 协议上限），无需手动分批。
- `WhereJsonbPathText/Eq` 的 key 与值均已参数化，key 可直接接用户输入（此前 key 被包成标识符，PG 上报 column not exist）；`SelectEx/SelectPrefix` 的排除参数传未转义字段名。
- 昂贵 debug 日志参数（如 args 的 JSON 序列化）用 `logkit.DebugEnabled()` 先判级再拼装，勿在调用点无条件求值。
- `WithSchema` 的拷贝持有创建那一刻的 TX 快照：在父 ds `BeginTX` 之前创建则其读写不进父事务，需要事务时先 BeginTX 再 WithSchema。

### Routing + OpenAPI

```go
router.Group("/user/login").Post("", Login).Api(
    openapi.Tag("user:用户模块"),
    openapi.Summary("登录"),
    openapi.ReqParam(loginParam{}),
    openapi.Response(ResLogin{}),
    openapi.Security(nil),  // nil = no auth required
)
```
- Mandatory: `Tag`, `Summary`.
- Guard with `middleware.AuthLogin()` / `AuthPerm()` / `AuthPermAll()` / `AuthRole()` / `AuthDept()`; open endpoints get `openapi.Security(nil)`.
- 鉴权失败与权限不足统一用 `context.ResultAuthErr`(401) 响应——本框架 RestRet 只有 0/401/500 三档，不单开 403。
- `main.go` 必须先调 `user.Init()`（注册 tokenkit 权限数据源）再 `restkit.AddActions(...)`；未注册时鉴权 **fail closed**，管理员接口对所有人关闭。

### Auth stack (pkg/service/tokenkit)

token 为**不透明随机串**（crypto/rand 32 字节 hex），不含载荷，状态全部以服务端会话记录为准。取代了原来的 `pkg/service/jwtkit`（已删除，含其 `jwt.*` 配置与 `jwt.secret` 密钥项）。换来三件事：剔出即时生效、无需签名密钥（消灭「空密钥」P0）、多端/顶号/在线列表天然是 Redis 原语。

存储布局（逻辑 key，redis 后端自动加 `redis.prefix`；未配置 `redis.host` 时退回进程内内存，仅限单机开发与单测）：

| key | 类型 | 内容 |
|---|---|---|
| `tk:t:<token>` | STRING | 会话 JSON，TTL = `token.idle` |
| `tk:u:<uid>` | SET | 该用户在线的 token 集合 |
| `tk:online` | ZSET | 全局在线索引，member=token，score=最后活跃时间（毫秒） |

- 两道独立过期闸门：`token.idle` 管「多久没动」（鉴权滑动续期；会话记录写入按「距上次落库超空闲窗口一半」节流，在线索引 score 每次鉴权都更新），`token.expire` 管「总共能活多久」（以 `LoginTime` 为基准，**续期无法延长**）。`token.multiLogin=false` 时新登录顶掉该用户全部旧会话。
- 公开 API：`Create/Parse/Refresh/Destroy/DestroyByUser/ListByUser/OnlineCount/ListOnline/ListOnlineOf/LastActive`。`Refresh` 返回 `(*Session, bool)`（鉴权中间件据此免二次解析）；**LastActive 的实时权威在 `tk:online` 的 score**，展示类读取（`LastActive/ListOnline/ListOnlineOf`）一律取 score，会话记录里的字段允许 idle/2 滞后。
- **用户 id 一律是不透明 `string`**（`Session.UserId` / `Principal.UserId` / `Loader` / `PrincipalOf` / `InvalidatePrincipal` 全部如此）。tokenkit 只把 id 当标识用于建索引与传参，**不做任何数值假设**——自增 bigint 由调用方 `strconv.FormatInt` 成十进制串，UUID/雪花 id 原样传入；业务层的 `int64` ↔ `string` 转换只发生在边界（mod/user 在 `service.uidOf` 与 `cast.ToInt64`）。
- HTTP 层取身份用 `ctx.GetUidStr() string`（首选）；`ctx.GetUid() int64` 仅供 id 确为数值型的业务层，**非数字 id 会静默返回 0，调用方须自行校验**。
- `OnlineSession.UserId` 刻意用外层 `int64` 字段遮蔽内层 tokenkit 的 `string` 版，让在线列表的 `userId` 与用户模块其余接口保持数字类型一致。
- `tokenkit/authz.go` 定义 `Principal`（UserId/Department/Roles/Privileges/Exclude）与 `HasPerm/HasPrivilege/HasRole/HasDept/IsDeptUnder`，**nil 一律拒绝（fail closed）**。`Principal` 有 1 分钟 `cachekit` 缓存，改角色/部门/剔除项后必须调 `tokenkit.InvalidatePrincipal(uid)`（角色变更用 `service.InvalidateRolePrincipals(roleId)` 反查受影响用户）。
- restkit 侧对应物：`ctx.GetToken/GetSession/GetUidStr/GetUid/IsLogin/RefreshToken/DestroyToken/SetTokenCookie/SessionOptions`（`context/token.go`）与 `ctx.Principal/HasPerm/HasPermAll/HasRole/HasDept/MustPerm`（`context/authz.go`）。
- 传给 service 层的是 `*tokenkit.Principal` 而非 `*context.Context`——service 不接触 HTTP 语义的上下文。
- `ListOnline` 会先按空闲窗口批量清理在线索引的僵尸成员再分页；页内逐条回读会话记录，**单页 limit 建议 ≤200**。

### Response format
```json
{"result": 0, "message": "", "data": {}, "total": 0}
```
- 0 = success, 401 = auth failure, 500 = business error (from `panic(exception.New(...))`, caught by `middleware.Recover`).
- `ctx.JsonSuccess(data...)`, `ctx.JsonSuccessWithPage(data, total)`, `ctx.JsonError(msg)`.

### Sensitive fields / security
- `Pwd` and similar must be `json:"-"`.
- Passwords use bcrypt (`cryptokit.HashPwd`/`CheckPwd`); legacy MD5 hashes are lazily upgraded to bcrypt on successful login (`cryptokit.NeedUpgrade`). Never write new MD5 password code.
- Never hardcode secrets/connection strings.
- Delete operations must nullify unique fields (phone, username) to avoid dirty data.

## Agent runtime (agent/)

eino ADK (`github.com/cloudwego/eino/adk`) based ReAct agent, exposed as `go-frame agent` REPL 子命令（cmd/agent.go）。

```
agent/runtime/model.go        NewChatModel — 按 llm.apiType 构造 openai / claude 模型
agent/runtime/runner.go       NewRunner — ChatModelAgent + 中间件编排：
                                filesystem 中间件（ls/read/write/edit/glob/grep/execute，经 windowsSafeBackend 包装：
                                GlobInfo/GrepRaw 对不存在目录返回空、Execute 用 resolveShell 替代硬编码 /bin/sh）、
                                skill 中间件（agent.skillsDir 存在时加载）、toolErrorSoftener（工具报错转为文本结果回给模型自纠，
                                而非 NodeRunError 崩溃）、rag_search 工具按需注册
agent/runtime/sessionstore/   会话历史 + perf 事件持久化（history.json / perf.json），session <id> 恢复、resume 恢复 checkpoint
agent/tools/ragflow.go        RAGFlow 检索工具（HTTP API，Bearer 认证）
```

- Config keys all optional & degrade gracefully（`pkg/cli/configkey/agent.go`、`ragflow.go`）；新 agent 配置项也放这里，经 `bind.go` 绑定为 cobra flag。
- 子命令注册走 `cli.AddChildCMD(AgentCMD())`（不依赖配置文件的用 `AddChildCMDWithoutConfig`，如 version）。
- Windows 约束集中在 `windowsSafeBackend`/`resolveShell`，勿在工具层散落 `runtime.GOOS` 分支。

## Adding a module

Follow the Layout & Conventions above (model → dao → service → controller). Wire the controller's `Init` into `mod.go` `All()` and register in `main.go`；模块若有自己的权限数据，在 `mod.go` 加 `Init()` 注册 `tokenkit.SetLoader`，并在 `main.go` 中于 `AddActions` 之前调用。 Verify: `go build ./... && go vet ./... && gofmt -l . && go test ./...`.

## Agent Rules

### Core Rules

- Always re-read the target file immediately before making any edit. Never rely on previously cached or summarized content when modifying code.
- Make only the minimal changes explicitly requested. Do not refactor, optimize, or modify unrelated code.
- Follow existing project conventions for naming, formatting, and architecture.

### PowerShell Encoding Rules

- Save all `.ps1` scripts as **UTF-8 with BOM**.
- Write Go source files (.go) as **UTF-8 without BOM** — `[System.IO.File]::WriteAllText($path, $content, [System.Text.UTF8Encoding]::new($false))`.
- Never use `Set-Content` or `Out-File` for .go files — they may emit UTF-16 or BOM-mangled output.
- When reading .go files into variables for editing, use `Get-Content -Raw -Encoding UTF8`.
- Include `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8` at the top of every .ps1 script.
- **Inline commands** (bash tool): every command that produces text output must start with `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8;` — a new PowerShell process defaults to gb2312 (CP936), which garbles UTF-8-decoded output.
- Prefer `pwsh`; if using `powershell.exe`, prepend UTF-8 encoding via `-Command`.
- Use `Out-File -Append -Encoding UTF8` instead of `>>` redirection.
