package sqlkit

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/example/go-frame/pkg/class/const/sqlconst"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/library/timekit"
	"github.com/example/go-frame/pkg/service/configkit"
	"github.com/example/go-frame/pkg/service/logkit"
	"github.com/jmoiron/sqlx"
)

type DataSource struct {
	// 用于postgres, 或oracle/dm
	Schema string
	// 数据源（事务时使用）
	TX *sqlx.Tx
	// 指定数据源（原始数据源连接池）
	DBPool *sqlx.DB
	Driver string
	// 事务嵌套深度，支持 TxArea 嵌套复用同一事务
	txDepth int
}

// ColumnSchema 表结构字段
type ColumnSchema struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Comment  string `json:"comment"`
}

var defaultDB *sqlx.DB

// DataSourceParam 创建数据源的参数
type DataSourceParam struct {
	Driver  string `json:"driver"`
	Host    string `json:"host"`
	Port    string `json:"port"`
	User    string `json:"username"`
	Pwd     string `json:"pwd"`
	Name    string `json:"db"`
	MaxOpen int    `json:"maxOpen"`
	MaxIdle int    `json:"maxIdle"`
	MaxLife int    `json:"maxLife"`
	// SSLMode PG/Kingbase 的 sslmode（disable/require/verify-ca/verify-full），空 = disable
	SSLMode string `json:"sslMode"`
	// Params mysql 驱动的系统变量参数（如 sql_mode），追加到 DSN，go-sql-driver 会在每个
	// 新连接上执行 SET 生效（连接池内连接均适用）；key/value 做 URL 转义。
	// 值为 SQL 值语义：字符串型自动补单引号（sql_mode 这类值务必不用自己加引号），
	// 数字与布尔关键字（true/false/on/off/default/null）原样保留，已带单引号的值不再处理
	Params map[string]string `json:"params"`
}

func getDataSourceName(p DataSourceParam) (string, string) {
	var param string
	switch p.Driver {
	case sqlconst.Postgres, sqlconst.Kingbase:
		ssl := p.SSLMode
		if ssl == "" {
			ssl = "disable"
		}
		// user/password 按 libpq 规则转义，含空格等特殊字符时不致破坏 DSN
		// connect_timeout：建连阶段超时，避免主机不可达/半开链路时无限阻塞在拨号上
		param = fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s connect_timeout=10", p.Host, p.Port, quotePGDSN(p.User), quotePGDSN(p.Pwd), p.Name, ssl)
		if p.Host == "" || p.Port == "" {
			panic(exception.New("sqlkit: database config error"))
		}
	case sqlconst.Mysql:
		// charset 用 utf8mb4；loc 取 timekit 的时区配置，避免硬编码
		// timeout：建连阶段超时（拨号挂起保护）；params 显式配了同名键则不追加默认值
		param = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=true&loc=%s", p.User, p.Pwd, p.Host, p.Port, p.Name, url.QueryEscape(timekit.GetLocation().String()))
		if _, ok := p.Params["timeout"]; !ok {
			param += "&timeout=10s"
		}
		for k, v := range p.Params {
			// 驱动级参数（timeout/readTimeout/writeTimeout/interpolateParams 等）由
			// go-sql-driver 自己解析，值是 Go 形态（如 5m、true），不能按 SQL 字面量包单引号
			if driverLevelMysqlParam[k] {
				param += "&" + url.QueryEscape(k) + "=" + url.QueryEscape(v)
			} else {
				param += "&" + url.QueryEscape(k) + "=" + url.QueryEscape(quoteParamSQLValue(v))
			}
		}
		if p.Host == "" || p.Port == "" {
			panic(exception.New("sqlkit: database config error"))
		}
	case sqlconst.SqlServer:
		param = fmt.Sprintf("server=%s;user id=%s;password=%s;port=%s;database=%s", p.Host, p.User, p.Pwd, p.Port, p.Name)
		if p.Host == "" || p.Port == "" {
			panic(exception.New("sqlkit: database config error"))
		}
	case sqlconst.Sqlite3:
		param = p.Name
		if p.Name == "" {
			panic(exception.New("sqlkit: dbName error"))
		}
	case sqlconst.DM:
		// https://eco.dameng.com/document/dm/zh-cn/pm/go-rogramming-guide.html
		param = fmt.Sprintf("dm://%s:%s@%s:%s", p.User, p.Pwd, p.Host, p.Port)
		if p.Host == "" || p.Port == "" {
			panic(exception.New("sqlkit: database config error"))
		}
	case sqlconst.TaosSql:
		param = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s", p.User, p.Pwd, p.Host, p.Port, p.Name)
		if p.Host == "" || p.Port == "" {
			panic(exception.New("sqlkit: database config error"))
		}
	case sqlconst.TaosWS:
		param = fmt.Sprintf("%s:%s@ws(%s:%s)/%s", p.User, p.Pwd, p.Host, p.Port, p.Name)
		if p.Host == "" || p.Port == "" {
			panic(exception.New("sqlkit: database config error"))
		}
	default:
		panic(exception.New("driver not supported"))
	}
	return p.Driver, param
}

// quotePGDSN libpq 关键字/值形式 DSN 的值转义：含空白、单引号、反斜杠时用单引号包裹，
// 值内的 \ 与 ' 前加反斜杠，避免密码含特殊字符时破坏 DSN。
func quotePGDSN(v string) string {
	if v == "" {
		return v
	}
	if strings.ContainsAny(v, " \t\n\r'\\") {
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, `'`, `\'`)
		return "'" + v + "'"
	}
	return v
}

// driverLevelMysqlParam go-sql-driver 自身的 DSN 参数（非 SET 系统变量）：值由驱动解析，
// 保持 Go 形态（duration/bool），不经过 quoteParamSQLValue 的 SQL 字面量引号包裹
var driverLevelMysqlParam = map[string]bool{
	"timeout": true, "readTimeout": true, "writeTimeout": true,
	"allowNativePasswords": true, "allowAllFiles": true, "checkConnLiveness": true,
	"clientFoundRows": true, "columnsNumberAlias": true, "interpolateParams": true,
	"maxAllowedPacket": true, "multiStatements": true, "parseTime": true,
	"rejectReadOnly": true, "serverRSAPublicKeyFile": true, "strict": true,
	"tls": true, "charset": true, "collation": true, "lru_cache_size": true,
}

// quoteParamSQLValue 归一 mysql 系统变量参数值：go-sql-driver 把 DSN 参数原样拼成
// `SET <key> = <value>`，字符串型变量必须自带单引号——否则值里的逗号会被当成下一个变量的
// 起始（sql_mode=ONLY_FULL_GROUP_BY,NO_ENGINE_SUBSTITUTION → Error 1064 near ”，且发生在
// 建连阶段直接 panic）。已用单引号包裹的值、数字与布尔关键字保持原样，其余包单引号并转义内部
// 反斜杠与单引号。
func quoteParamSQLValue(v string) string {
	if v == "" {
		return v
	}
	if len(v) >= 2 && strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'") {
		return v
	}
	if isBareSQLValue(v) {
		return v
	}
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, "'", "''")
	return "'" + v + "'"
}

// isBareSQLValue 判断参数值能否不加引号直接出现在 SET 右侧：数字（$ 前后缀、Inf、NaN 等
// ParseFloat 能吞下的非数字写法先按首字符挡掉）或布尔/状态关键字。
func isBareSQLValue(v string) bool {
	switch strings.ToLower(v) {
	case "true", "false", "on", "off", "default", "null":
		return true
	}
	if c := v[0]; (c < '0' || c > '9') && c != '+' && c != '-' && c != '.' {
		return false
	}
	_, err := strconv.ParseFloat(v, 64)
	return err == nil
}

// NewDataSource 创建一个数据源
func NewDataSource(param DataSourceParam) *DataSource {
	db := getDB(param)
	ds := &DataSource{
		Driver: param.Driver,
		DBPool: db,
	}
	if sqlconst.IsPostgresType(param.Driver) {
		ds.Schema = "public"
	} else if sqlconst.IsSingleDBSchema(param.Driver) {
		ds.Schema = param.Name
	}
	return ds
}

func getDB(param DataSourceParam) *sqlx.DB {
	var db *sqlx.DB
	if param.Driver == sqlconst.Sqlite3 {
		db = sqlx.MustOpen(getDataSourceName(param))
		// 开启 WAL 模式，适用本地磁盘/NAS 均可
		db.Exec("PRAGMA journal_mode=WAL")
		db.Exec("PRAGMA busy_timeout=5000")  // 避免锁冲突直接报错
		db.Exec("PRAGMA synchronous=NORMAL") // WAL 下 NORMAL 已足够安全，进一步提升写入性能
		// WAL 已缓解写锁争用；如遇 database is locked 可再限 SetMaxOpenConns(1)
	} else {
		db = sqlx.MustConnect(getDataSourceName(param))
		if param.MaxLife > 0 {
			db.SetConnMaxLifetime(time.Duration(param.MaxLife) * time.Minute)
		}
		if param.MaxOpen > 0 {
			db.SetMaxOpenConns(param.MaxOpen)
		}
		if param.MaxIdle > 0 {
			db.SetMaxIdleConns(param.MaxIdle)
		}
	}
	err := db.Ping()
	if err != nil {
		// 有些数据库可能 ping 不可用但本身能正常查询，仅打印错误日志，不抛异常中断流程
		logkit.Error("db: " + param.Name + " ping failed: " + err.Error())
	} else {
		logkit.Info("db: " + param.Name + " ping success")
	}
	return db
}

var once sync.Once

func DefaultDataSource() *DataSource {
	once.Do(func() {
		defaultDB = getDB(DataSourceParam{
			Driver:  configkit.GetString(configkey.DBDriver),
			Host:    configkit.GetString(configkey.DBHost),
			Port:    configkit.GetString(configkey.DBPort),
			User:    configkit.GetString(configkey.DBUser),
			Pwd:     configkit.GetString(configkey.DBPwd),
			Name:    configkit.GetString(configkey.DBName),
			MaxOpen: configkit.GetInt(configkey.DBMaxOpen),
			MaxIdle: configkit.GetInt(configkey.DBMaxIdle),
			MaxLife: configkit.GetInt(configkey.DBMaxLife),
			SSLMode: configkit.GetString(configkey.DBSSLMode),
		})
	})
	driver := configkit.GetString(configkey.DBDriver)
	ds := &DataSource{
		Driver: driver,
		DBPool: defaultDB,
	}
	if sqlconst.IsPostgresType(driver) {
		ds.Schema = "public"
	} else {
		ds.Schema = configkit.GetString(configkey.DBName)
	}
	return ds
}

// DecoTableName 获取 schema 修饰的转义的tableName
func (ds *DataSource) DecoTableName(tableName string) string {
	s := ""
	if sqlconst.IsPostgresType(ds.Driver) {
		if ds.Schema != "" {
			s = ds.EscapeName(ds.Schema) + "."
		} else {
			s = "public."
		}
	} else if ds.Schema != "" && sqlconst.IsSingleDBSchema(ds.Driver) {
		s = ds.EscapeName(ds.Schema) + "."
	}
	return s + ds.EscapeName(tableName)
}

// EscapeName 表名列名的转义符添加。标识符内部的定界符按 SQL 规则翻倍：
// mysql/taos 的 ` 与 PG/Oracle/DM 的 " 成对出现，内部字面量必须写两遍
// （列名本身含反引号时不转义会被提前闭合，报 1064 near '`'）。
func (ds *DataSource) EscapeName(name string) string {
	switch ds.Driver {
	case sqlconst.Mysql:
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	case sqlconst.TaosWS, sqlconst.TaosSql:
		// key=tabname时，特殊处理，不加``，如果加了就代表是自定义表字段
		if name == "tbname" {
			return name
		}
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	case sqlconst.DM, sqlconst.Oracle:
		// 注意大写了
		return `"` + strings.ReplaceAll(strings.ToUpper(name), `"`, `""`) + `"`
	default:
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	}
}

// Commit 提交事务。支持嵌套：仅在最外层（txDepth 归零）时真正提交。
func (ds *DataSource) Commit() {
	if ds.TX == nil {
		return
	}
	if ds.txDepth > 0 {
		ds.txDepth--
	}
	if ds.txDepth > 0 {
		return
	}
	err := ds.TX.Commit()
	ds.TX = nil
	if err != nil {
		panic(exception.New(err.Error()))
	}
}

// Rollback 回滚事务。重置 TX 与嵌套深度，支持在任意层级触发回滚。
func (ds *DataSource) Rollback() {
	if ds.TX == nil {
		return
	}
	err := ds.TX.Rollback()
	ds.TX = nil
	ds.txDepth = 0
	if err != nil {
		panic(exception.New(err.Error()))
	}
}

// BeginTX 开启事务并赋值到 ds.TX，使后续 Query/Exec 走事务。
// 支持嵌套：已存在事务时复用，仅累加深度。
func (ds *DataSource) BeginTX() *sqlx.Tx {
	if ds.TX == nil {
		ds.TX = ds.DBPool.MustBegin()
	}
	ds.txDepth++
	return ds.TX
}

func (ds *DataSource) Query(sql string, args []any) *sqlx.Rows {
	return ds.QueryCtx(context.Background(), sql, args)
}

// QueryCtx 带 ctx 的查询，支持超时与取消传播。
func (ds *DataSource) QueryCtx(ctx context.Context, sql string, args []any) *sqlx.Rows {
	var rows *sqlx.Rows
	var err error
	if ds.TX != nil {
		rows, err = ds.TX.QueryxContext(ctx, sql, args...)
	} else {
		rows, err = ds.DBPool.QueryxContext(ctx, sql, args...)
	}
	if err != nil {
		panic(exception.New(err.Error()+" ["+sql+"]", 2))
	}
	return rows
}

func (ds *DataSource) Exec(sql string, args []any) sql.Result {
	return ds.ExecCtx(context.Background(), sql, args)
}

// ExecCtx 带 ctx 的执行，支持超时与取消传播。
func (ds *DataSource) ExecCtx(ctx context.Context, sql string, args []any) sql.Result {
	if ds.TX != nil {
		return ds.TX.MustExecContext(ctx, sql, args...)
	} else {
		return ds.DBPool.MustExecContext(ctx, sql, args...)
	}
}

// WithSchema S9: 返回一个浅拷贝的 DataSource，仅替换 Schema，共享原 DBPool/TX。
// 用于请求级 schema 注入，避免在业务代码中反复改写 `dao.DataSource().Schema`。
// （历史示例从 JWT 载荷取 schema；改为不透明 token 后不再有载荷，
// schema 应从会话记录或请求头等其它渠道取得后显式传入。）
//
// 注意事务陷阱：拷贝持有的是创建那一刻父 ds 的 TX 快照。若在父 ds BeginTX 之前
// 创建拷贝，拷贝内 TX 为 nil，其后的读写不进父事务；需要在事务内使用时，
// 请在 BeginTX 之后再 WithSchema。
func (ds *DataSource) WithSchema(schema string) *DataSource {
	return &DataSource{
		Schema:  schema,
		TX:      ds.TX,
		DBPool:  ds.DBPool,
		Driver:  ds.Driver,
		txDepth: ds.txDepth,
	}
}
