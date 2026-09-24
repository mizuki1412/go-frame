# AGENTS.md

Go scaffold for vibe-coding REST services (gin + viper + cobra + sqlx/squirrel + PostgreSQL), plus an eino-ADK agent demo. Repo docs/comments are in Chinese; match that style.

## Must read first

`doc/effective_go.md` — 编写本项目代码的规范依据（三源融合知识图谱：官方 Effective Go + 《Go专家编程》 + 《Mastering Go》，末章含本项目对照审计清单）。

## Current state (verified)

- Build, vet and tests all green (verified 2026-09-21, after `go mod tidy` filled in the go.sum entries for the new eino deps). The required verification for every change is:
  ```
  go build ./... && go vet ./... && gofmt -l . && go test ./...
  ```
  Caveat: on a Windows checkout with `core.autocrlf=true`, `gofmt -l .` lists nearly every file because files are CRLF on disk (repo stores LF). `gofmt -d` shows content-identical diffs — treat that as clean; only real formatting diffs count.
- Tests: pkg infra (`pkg/class`, `pkg/library/cmdkit`, `pkg/library/cryptokit`, `pkg/library/framekit`, `pkg/service/jwtkit`, …) plus `agent/runtime/sessionstore` and `pkg/cli` (bind_agent_test); no `mod/` tests, no lint config, no CI.
- Module path `github.com/example/go-frame` and project name `go-frame` are placeholders; rename (go.mod module path + import prefixes, at minimum) before reuse.
- Go toolchain: go 1.27.0 (go.mod `go 1.27.0`).
- Business modules: `mod/user/`. Agent runtime lives outside `mod/` in `agent/` (infra-like, may not be imported by `mod/*`).

## Layout

```
pkg/                          # reusable infra, MUST NOT depend on mod/*
  class/                        nullable DB wrappers: String, Int64, Time, Decimal, ArrInt, MapString, File, etc.
  library/*kit/                 pure utility packages
  service/*kit/                 infra kits: restkit, sqlkit, configkit, logkit, jwtkit, rediskit, aikit, mqttkit, netkit, cachekit, cronkit, excelkit, pdfkit, serialkit, storagekit
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
main.go                       # cli.RootCMD(...) → restkit.AddActions(user.All()...) → restkit.Run()
```

## User module data model (mod/user)

- `sys_user` 单列 `department`（部门决定数据范围）；角色为**多对多**：`sys_user_role` 中间表（组合主键 userid+roleid，无逻辑删除），`User.Roles []*Role` 无 db tag，仅由 `userdao` 的 `NewCascadeManyLink` 经中间表批量装配（整批 2 条 SQL）。角色为全局对象，`Role` **不再有 Department**。
- `immutable`（user/role/department）是正式列，不再塞 Extend；`Extend` 只放 `privilegeExclude`（`User.HasPrivilege` = 多角色并集 − 剔除）。`role id=0` 为内置超级管理员（`model.RoleIdSuperAdmin`），持有者禁止经管理员接口改绑/删除。
- 角色绑定为整体替换语义（`userroledao.ReplaceForUser`：先删后插），AddUser/UpdateUser 中用户行与绑定同事务（`sqlkit.TxArea` + 用 `targetDS` 构造 dao）。
- 四张业务表均有 `createdt`/`updatedt`，服务层每次 `UpdateObj` 前显式 `UpdateDt.Set(time.Now())`（sqlkit 不自动填充）。

## Config

- Every key is a `const` in `pkg/cli/configkey/*.go`, bound as cobra flag in `pkg/cli/bind.go`, read via `configkit.GetString/GetInt/GetBool(key, default...)` — never read viper directly.
- Defaults: `:10000` for REST server; `/v3/api-docs` serves the OpenAPI JSON (no bundled UI).
- Config file: `config.yaml` in working dir, override with `-c/--config`.
- Env placeholders: after `ReadInConfig`, `loadConfig` expands `${ENV_NAME}` in string config values to `os.Getenv(ENV_NAME)`; unset vars expand to empty string (2026-09-19, VERSION 20260919).
- Key families (each in its own `configkey/*.go`): rest/db/jwt/redis/... plus the agent stack:
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
- Guard with `middleware.AuthJWT()`; open endpoints get `openapi.Security(nil)`.
- All auth endpoints check JWT (stores uid only); logout via `ctx.DestroyJwt()`.
- Session model (sliding): a server-side whitelist key `token:<raw-token>` gates every authenticated request. `jwt.idle` (default 1h) is the idle window — `AuthJWT` renews it via `cachekit.Renew` on each authenticated request; `jwt.expire` (default 168h) is the absolute cap baked into the JWT exp. Set `jwt.idle<=0` to disable sliding (TTL = expire, legacy behavior).
- Token domain isolation: guard admin-domain routes with `middleware.AuthJWTScope("admin")` and issue those tokens via `jwtkit.New(uid, "admin")` — app and admin tokens share one secret/whitelist, so `Claims.Scope` is what keeps them from crossing over.

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

Follow the Layout & Conventions above (model → dao → service → controller). Wire the controller's `Init` into `mod.go` `All()` and register in `main.go`. Verify: `go build ./... && go vet ./... && gofmt -l . && go test ./...`.

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
