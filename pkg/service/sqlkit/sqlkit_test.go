package sqlkit

import (
	"strings"
	"testing"
	"time"

	"github.com/example/go-frame/pkg/class"
	"github.com/example/go-frame/pkg/class/const/sqlconst"
)

// 免 DB 的 SQL 生成单测：直接构造 Dao（不触发 DefaultDataSource 连接），
// 断言 builder 生成的 SQL/args，覆盖各修复点的回归。

type sqlkitUser struct {
	Id       int64         `db:"id" pk:"true" auto:"true" table:"sys_user"`
	Username string        `db:"username"`
	Pwd      string        `db:"pwd"`
	Extend   string        `db:"extend"`
	Del      bool          `db:"deleted" logicDel:"true"`
	Birthday class.Time    `db:"birthday"`
	Score    class.Decimal `db:"score" precision:"2"`
}

func newSqlkitDao(driver string) Dao[sqlkitUser] {
	dao := Dao[sqlkitUser]{}
	dao.dataSource = &DataSource{Driver: driver}
	dao.modelMeta = dao.modelMeta.init(sqlkitUser{}, dao.dataSource)
	return dao
}

// S 系列修复回归：SelectEx 的排除参数按未转义名（OriKey）比对。
// 此前与 escape 后的列名 Contains 比对，永不命中，排除失效。
func TestSelectExExcludesByOriKey(t *testing.T) {
	for _, driver := range []string{sqlconst.Postgres, sqlconst.Mysql} {
		sql, _ := newSqlkitDao(driver).SelectEx("pwd").Sql()
		if strings.Contains(sql, "pwd") {
			t.Fatalf("%s: SelectEx(pwd) 仍选中了 pwd: %s", driver, sql)
		}
		if !strings.Contains(sql, `"username"`) && !strings.Contains(sql, "`username`") {
			t.Fatalf("%s: SelectEx 丢掉了其他列: %s", driver, sql)
		}
	}
}

// jsonb 谓词的 key 必须参数化。此前 key 经 EscapeName 变成标识符 "key"，
// PG 上报 column "key" does not exist，且不清洗内嵌引号存在注入面。
func TestWhereJsonbPathEqParameterized(t *testing.T) {
	sql, args := newSqlkitDao(sqlconst.Postgres).Select().
		WhereJsonbPathEq("extend", "nickname", "x").Sql()
	if !strings.Contains(sql, `"extend"->>$1 = $2`) {
		t.Fatalf("jsonb key 未参数化: %s", sql)
	}
	if strings.Contains(sql, `"nickname"`) {
		t.Fatalf("jsonb key 被拼成标识符: %s", sql)
	}
	if len(args) != 2 || args[0] != "nickname" || args[1] != "x" {
		t.Fatalf("jsonb 参数错误: %v", args)
	}
	// PG 占位符转 Dollar
	if !strings.Contains(sql, "$1") || !strings.Contains(sql, "$2") {
		t.Fatalf("PG 占位符错误: %s", sql)
	}
}

// FromSubQuery 生成的子查询必须自带 FROM。
// 此前直接嵌入 sub.builder（From 在终结阶段才落地），子查询缺 FROM。
func TestFromSubQueryIncludesFrom(t *testing.T) {
	dao := newSqlkitDao(sqlconst.Postgres)
	sub := dao.Select("id").Where("id>?", int64(1))
	sql, _ := dao.Select().FromSubQuery(sub, "t").Where("t.id>?", int64(2)).Sql()
	if !strings.Contains(sql, "(SELECT id FROM") && !strings.Contains(sql, "SELECT id FROM") {
		t.Fatalf("子查询缺 FROM: %s", sql)
	}
	if !strings.Contains(sql, ") AS t") && !strings.Contains(sql, ") AS \"t\"") && !strings.Contains(sql, "AS t") {
		t.Fatalf("子查询别名缺失: %s", sql)
	}
}

// InsertDao.Select 生成的 insert ... select 中，select 子查询必须自带 FROM。
func TestInsertSelectIncludesFrom(t *testing.T) {
	dao := newSqlkitDao(sqlconst.Postgres)
	sub := dao.Select("username").Where("id>?", int64(1))
	sql, _ := dao.Insert().Columns("username").Select(sub).Sql()
	if !strings.Contains(sql, "SELECT username FROM") {
		t.Fatalf("insert...select 子查询缺 FROM: %s", sql)
	}
	if !strings.Contains(sql, "INSERT INTO") {
		t.Fatalf("insert 语句缺失: %s", sql)
	}
}

// UpsertObj 生成 on conflict/on duplicate key 语句，更新列不含主键/自增与冲突列。
func TestBuildUpsertSql(t *testing.T) {
	u := &sqlkitUser{Id: 1, Username: "u1", Pwd: "p"}
	dao := newSqlkitDao(sqlconst.Postgres)
	sql, _ := dao.buildUpsert(u, "username").Sql()
	if !strings.Contains(sql, "on conflict (\"username\") do update set") {
		t.Fatalf("PG upsert 语句错误: %s", sql)
	}
	if strings.Contains(sql, "\"id\"=excluded") {
		t.Fatalf("upsert 更新列不应包含自增主键: %s", sql)
	}
	if strings.Contains(sql, "\"username\"=excluded") {
		t.Fatalf("upsert 更新列不应包含冲突列: %s", sql)
	}
	if !strings.Contains(sql, "\"pwd\"=excluded.\"pwd\"") {
		t.Fatalf("upsert 更新列缺失: %s", sql)
	}

	mysql := newSqlkitDao(sqlconst.Mysql)
	sql, _ = mysql.buildUpsert(u, "username").Sql()
	if !strings.Contains(sql, "on duplicate key update") {
		t.Fatalf("MySQL upsert 语句错误: %s", sql)
	}
	if !strings.Contains(sql, "`pwd`=values(`pwd`)") {
		t.Fatalf("MySQL upsert 更新列错误: %s", sql)
	}
}

// 只设置批量级联时，单条 list 也应走批量（此前 len==1 且无 Cascade 时静默跳过）。
func TestCascadeListSingleWithBatchOnly(t *testing.T) {
	dao := newSqlkitDao(sqlconst.Postgres)
	called := 0
	dao.CascadeBatch = func(list []*sqlkitUser) { called++ }
	list := []*sqlkitUser{{Username: "a"}}
	cascadeList(dao, list)
	if called != 1 {
		t.Fatalf("仅设 CascadeBatch 时单条 list 未走批量: called=%d", called)
	}
}

// argsWrap 快速路径：无需转换时返回原切片，不重新分配。
func TestArgsWrapFastPath(t *testing.T) {
	args := []any{"a", 1, int64(2)}
	got := argsWrap(sqlconst.Postgres, args)
	if len(got) != 3 {
		t.Fatalf("argsWrap 改变了参数个数: %v", got)
	}
	// PG 下 class.Time 转 time.Time
	pgArgs := []any{class.Time{}, "s"}
	out := argsWrap(sqlconst.Postgres, pgArgs)
	if _, ok := out[0].(time.Time); !ok {
		t.Fatalf("PG 下 class.Time 未转为 time.Time: %T", out[0])
	}
	// sqlite 下 time.Time 转毫秒时间戳
	out = argsWrap(sqlconst.Sqlite3, []any{time.Now()})
	if _, ok := out[0].(int64); !ok {
		t.Fatalf("sqlite 下 time.Time 未转毫秒: %T", out[0])
	}
}

// taos 占位符改写：最后一个占位符之后的字面量须保留。
func TestHandlePlaceholderInWhereTrailingLiteral(t *testing.T) {
	pred := "a>? and b>0"
	got := handlePlaceholderInWhere(sqlconst.TaosSql, pred, 1)
	if got != "a>? and b>0" {
		t.Fatalf("尾部字面量被丢弃: %q", got)
	}
	got = handlePlaceholderInWhere(sqlconst.TaosSql, "a>? and b>?", "s", 2)
	if got != "a>'?' and b>?" {
		t.Fatalf("字符串参数占位符改写错误: %q", got)
	}
}

// Replace 仅 MySQL/SQLite 支持，其他驱动提前报错。
func TestReplaceDriverGuard(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("PG 上 Replace 应 panic")
		}
	}()
	newSqlkitDao(sqlconst.Postgres).Replace()
}

func TestInsertBatchChunkSize(t *testing.T) {
	if got := insertBatchChunkSize(3); got != 10922 {
		t.Fatalf("chunk size 错误: %d", got)
	}
	if got := insertBatchChunkSize(0); got != 1 {
		t.Fatalf("chunk size 下限错误: %d", got)
	}
	if got := insertBatchChunkSize(100000); got != 1 {
		t.Fatalf("chunk size 下限错误: %d", got)
	}
}

// mysql 系统变量参数值的引号归一：go-sql-driver 把 DSN 参数原样拼成 SET <key> = <value>，
// 字符串型变量若不自带单引号，值里的逗号会被当成下一个变量的起始
// （sql_mode=ONLY_FULL_GROUP_BY,NO_ENGINE_SUBSTITUTION → Error 1064 near ”，建连即 panic）。
func TestQuoteParamSQLValue(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ONLY_FULL_GROUP_BY,NO_ENGINE_SUBSTITUTION", "'ONLY_FULL_GROUP_BY,NO_ENGINE_SUBSTITUTION'"},
		{"+08:00", "'+08:00'"},
		{"'ANSI'", "'ANSI'"},
		{"1", "1"},
		{"0.5", "0.5"},
		{"-1", "-1"},
		{"1e5", "1e5"},
		{"true", "true"},
		{"OFF", "OFF"},
		{"", ""},
		{`O'Brien\path`, `'O''Brien\\path'`},
		{"NaN", "'NaN'"},
	}
	for _, c := range cases {
		if got := quoteParamSQLValue(c.in); got != c.want {
			t.Fatalf("quoteParamSQLValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// DSN 端到端：sql_mode 的值以 %27 包裹（驱动 QueryUnescape 后即带单引号的字符串字面量），
// 数字型参数不被误加引号。
func TestMysqlDSNQuotesParamValue(t *testing.T) {
	_, dsn := getDataSourceName(DataSourceParam{
		Driver: sqlconst.Mysql, Host: "127.0.0.1", Port: "3306", User: "u", Pwd: "p", Name: "db",
		Params: map[string]string{
			"sql_mode":   "ONLY_FULL_GROUP_BY,NO_ENGINE_SUBSTITUTION",
			"autocommit": "1",
		},
	})
	if !strings.Contains(dsn, "sql_mode=%27ONLY_FULL_GROUP_BY%2CNO_ENGINE_SUBSTITUTION%27") {
		t.Fatalf("sql_mode 未按字符串变量加引号: %s", dsn)
	}
	if !strings.Contains(dsn, "autocommit=1") {
		t.Fatalf("数字型参数被误加引号: %s", dsn)
	}
}

// DSN 超时护栏：mysql 未显式配 timeout 时默认追加建连超时；驱动级参数（readTimeout 等）
// 由 go-sql-driver 解析，值保持 Go 形态（duration/bool）不加 SQL 单引号；系统变量仍加引号。
func TestMysqlDSNTimeoutParams(t *testing.T) {
	_, dsn := getDataSourceName(DataSourceParam{
		Driver: sqlconst.Mysql, Host: "127.0.0.1", Port: "3306", User: "u", Pwd: "p", Name: "db",
		Params: map[string]string{
			"readTimeout":       "30s",
			"lock_wait_timeout": "60",
			"sql_mode":          "STRICT_TRANS_TABLES,NO_ZERO_DATE",
		},
	})
	if !strings.Contains(dsn, "&timeout=10s") {
		t.Fatalf("未默认追加建连 timeout: %s", dsn)
	}
	// 驱动级参数：值 30s 原样（URL 转义后 30s，%30s 之类不应出现引号包裹）
	if !strings.Contains(dsn, "readTimeout=30s") {
		t.Fatalf("驱动级参数 readTimeout 被误加引号: %s", dsn)
	}
	// 系统变量：数字型仍走原样（isBareSQLValue），字符串型 sql_mode 需带 %27 引号
	if !strings.Contains(dsn, "sql_mode=%27STRICT_TRANS_TABLES%2CNO_ZERO_DATE%27") {
		t.Fatalf("系统变量 sql_mode 未按字符串加引号: %s", dsn)
	}
	// 显式配了 timeout 时不重复追加默认值
	_, dsn2 := getDataSourceName(DataSourceParam{
		Driver: sqlconst.Mysql, Host: "127.0.0.1", Port: "3306", User: "u", Pwd: "p", Name: "db",
		Params: map[string]string{"timeout": "5m"},
	})
	if strings.Contains(dsn2, "&timeout=10s") {
		t.Fatalf("显式配置 timeout 后仍追加默认值: %s", dsn2)
	}
	if !strings.Contains(dsn2, "timeout=5m") {
		t.Fatalf("显式 timeout 未原样保留: %s", dsn2)
	}
}

// EscapeName 的标识符内部定界符必须翻倍：datax 迁移时源表列名本身含反引号
// （如 `concat(scjj.`统一社会信用代码`, now(), '330903')`），只包外层会被提前闭合，
// 整条 SQL 报 1064 near '`'。
func TestEscapeNameEscapesDelimiter(t *testing.T) {
	cases := []struct{ driver, name, want string }{
		{sqlconst.Mysql, "id", "`id`"},
		{sqlconst.Mysql, "统一社会信用代码", "`统一社会信用代码`"},
		{sqlconst.Mysql, "co`l", "`co``l`"},
		{sqlconst.Mysql, "concat(scjj.`统一社会信用代码`, now(), '330903')",
			"`concat(scjj.``统一社会信用代码``, now(), '330903')`"},
		{sqlconst.Postgres, "co\"l", `"co""l"`},
		{sqlconst.Kingbase, "a\"b", `"a""b"`},
		{sqlconst.Oracle, "co\"l", `"CO""L"`},
		{sqlconst.DM, `x"y`, `"X""Y"`},
		{sqlconst.SqlServer, "co\"l", `"co""l"`},
		// sqlite3 走 default 分支，定界符是双引号：名字里的反引号不是定界符，无需处理
		{sqlconst.Sqlite3, "a`b\"c", "\"a`b\"\"c\""},
	}
	for _, c := range cases {
		ds := &DataSource{Driver: c.driver}
		if got := ds.EscapeName(c.name); got != c.want {
			t.Fatalf("%s EscapeName(%q) = %q, want %q", c.driver, c.name, got, c.want)
		}
	}
}
