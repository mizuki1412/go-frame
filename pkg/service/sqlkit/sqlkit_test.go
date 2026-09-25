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
