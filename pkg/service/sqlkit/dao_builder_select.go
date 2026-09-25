package sqlkit

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Masterminds/squirrel"
	"github.com/example/go-frame/pkg/class/const/sqlconst"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/library/jsonkit"
	"github.com/example/go-frame/pkg/service/logkit"
)

type SelectDao[T any] struct {
	Dao[T]
	builder        squirrel.SelectBuilder
	fromAs         string // 别名 from用默认的
	from           string // fromAs无效
	fromSet        bool   // builder 上已落过 From（From/FromSubQuery），applyFrom 不再覆盖
	ignoreLogicDel bool
}

type SubQueryInterface interface {
	// 默认占位符的，一般用于子查询
	sqlOriginPlaceholder() (string, []any)
}

func (dao SelectDao[T]) Print() {
	sql, args := dao.Sql()
	logkit.Info("sql print", "sql", sql, "args", jsonkit.ToString(args))
}

func (dao SelectDao[T]) sqlOriginPlaceholder() (string, []any) {
	dao = dao.applyFrom()
	dao.builder = dao.builder.PlaceholderFormat(squirrel.Question)
	return dao.builder.MustSql()
}

// applyFrom 把 From/FromAs/默认表名落到 builder 上。普通终结方法在 ToSql 阶段调用；
// 子查询需要自带 FROM 的场景（FromSubQuery、InsertDao.Select 等）在组装时提前调用。
// builder 上已落过 From（fromSet）时跳过，避免覆盖 FromSelect 的子查询表源。
func (dao SelectDao[T]) applyFrom() SelectDao[T] {
	if dao.fromSet {
		return dao
	}
	if dao.from != "" {
		dao.builder = dao.builder.From(dao.from)
	} else if dao.fromAs != "" {
		dao.builder = dao.builder.From(dao.modelMeta.getTable(dao.dataSource, dao.fromAs))
	} else {
		dao.builder = dao.builder.From(dao.modelMeta.getTable(dao.dataSource))
	}
	return dao
}

func (dao SelectDao[T]) Sql() (string, []any) {
	sqls, args, err := dao.ToSql()
	if err != nil {
		panic(exception.New(err.Error()))
	}
	return sqls, args
}
func (dao SelectDao[T]) ToSql() (string, []any, error) {
	dao = dao.applyFrom()
	dao.builder = dao.builder.PlaceholderFormat(placeholder(dao.dataSource.Driver))
	sqls, args, err := dao.builder.ToSql()
	return sqls, argsWrap(dao.dataSource.Driver, args), err
}

func (dao SelectDao[T]) IgnoreLogicDel() SelectDao[T] {
	dao.ignoreLogicDel = true
	return dao
}

// Prefix 在 sql 前写入语句
func (dao SelectDao[T]) Prefix(sql string, args ...any) SelectDao[T] {
	dao.builder = dao.builder.Prefix(sql, args...)
	return dao
}
func (dao SelectDao[T]) PrefixExpr(expr squirrel.Sqlizer) SelectDao[T] {
	dao.builder = dao.builder.PrefixExpr(expr)
	return dao
}
func (dao SelectDao[T]) Suffix(sql string, args ...any) SelectDao[T] {
	dao.builder = dao.builder.Suffix(sql, args...)
	return dao
}
func (dao SelectDao[T]) SuffixExpr(expr squirrel.Sqlizer) SelectDao[T] {
	dao.builder = dao.builder.SuffixExpr(expr)
	return dao
}

// Columns select 中额外增加 column
func (dao SelectDao[T]) Columns(cs ...string) SelectDao[T] {
	dao.builder = dao.builder.Columns(cs...)
	return dao
}

func (dao SelectDao[T]) RemoveColumns() SelectDao[T] {
	dao.builder = dao.builder.RemoveColumns()
	return dao
}
func (dao SelectDao[T]) From(from string) SelectDao[T] {
	dao.from = from
	dao.fromSet = true
	dao.builder = dao.builder.From(from)
	return dao
}
func (dao SelectDao[T]) FromAs(alias string) SelectDao[T] {
	dao.fromAs = alias
	return dao
}
// FromSubQuery 以子查询为 FROM 表。子查询先 applyFrom 保证自带 FROM 子句
// （此前直接嵌入 builder，生成的子查询没有 FROM，SQL 非法）。
func (dao SelectDao[T]) FromSubQuery(sub SelectDao[T], alias string) SelectDao[T] {
	sub = sub.applyFrom()
	dao.builder = dao.builder.FromSelect(sub.builder, alias)
	dao.fromSet = true
	return dao
}

// As S12: 生成 "(<本查询>) AS alias" 的 SQL 片段与参数，便于嵌入到其他语句
// （如作为 CTE 体、IN 子查询、JOIN 子查询）。占位符使用与当前数据源一致。
func (dao SelectDao[T]) As(alias string) (string, []any) {
	sql, args := dao.sqlOriginPlaceholder()
	return "(" + sql + ") AS " + alias, args
}

func (dao SelectDao[T]) Distinct() SelectDao[T] {
	dao.builder = dao.builder.Distinct()
	return dao
}

// Options adds select option to the query
func (dao SelectDao[T]) Options(options ...string) SelectDao[T] {
	dao.builder = dao.builder.Options(options...)
	return dao
}

func (dao SelectDao[T]) Join(dm DaoModelMeta, as string, on string, rest ...any) SelectDao[T] {
	dao.builder = dao.builder.Join(dm.getModelMeta().getTable(dm.getDataSource(), as)+" on "+on, rest...)
	return dao
}
func (dao SelectDao[T]) LeftJoin(dm DaoModelMeta, as string, on string, rest ...any) SelectDao[T] {
	dao.builder = dao.builder.LeftJoin(dm.getModelMeta().getTable(dm.getDataSource(), as)+" on "+on, rest...)
	return dao
}
func (dao SelectDao[T]) RightJoin(dm DaoModelMeta, as string, on string, rest ...any) SelectDao[T] {
	dao.builder = dao.builder.RightJoin(dm.getModelMeta().getTable(dm.getDataSource(), as)+" on "+on, rest...)
	return dao
}
func (dao SelectDao[T]) InnerJoin(dm DaoModelMeta, as string, on string, rest ...any) SelectDao[T] {
	dao.builder = dao.builder.InnerJoin(dm.getModelMeta().getTable(dm.getDataSource(), as)+" on "+on, rest...)
	return dao
}
func (dao SelectDao[T]) CrossJoin(dm DaoModelMeta, as string, on string, rest ...any) SelectDao[T] {
	dao.builder = dao.builder.CrossJoin(dm.getModelMeta().getTable(dm.getDataSource(), as)+" on "+on, rest...)
	return dao
}

func (dao SelectDao[T]) JoinRaw(join string, rest ...any) SelectDao[T] {
	dao.builder = dao.builder.Join(join, rest...)
	return dao
}
func (dao SelectDao[T]) LeftJoinRaw(join string, rest ...any) SelectDao[T] {
	dao.builder = dao.builder.LeftJoin(join, rest...)
	return dao
}
func (dao SelectDao[T]) RightJoinRaw(join string, rest ...any) SelectDao[T] {
	dao.builder = dao.builder.RightJoin(join, rest...)
	return dao
}
func (dao SelectDao[T]) InnerJoinRaw(join string, rest ...any) SelectDao[T] {
	dao.builder = dao.builder.InnerJoin(join, rest...)
	return dao
}
func (dao SelectDao[T]) CrossJoinRaw(join string, rest ...any) SelectDao[T] {
	dao.builder = dao.builder.CrossJoin(join, rest...)
	return dao
}

func (dao SelectDao[T]) Where(pred any, args ...any) SelectDao[T] {
	dao.builder = dao.builder.Where(handlePlaceholderInWhere(dao.dataSource.Driver, pred, args...), args...)
	return dao
}
func (dao SelectDao[T]) Having(pred any, args ...any) SelectDao[T] {
	dao.builder = dao.builder.Having(handlePlaceholderInWhere(dao.dataSource.Driver, pred, args...), args...)
	return dao
}

func (dao SelectDao[T]) GroupBy(groupBys ...string) SelectDao[T] {
	dao.builder = dao.builder.GroupBy(dao.modelMeta.escapeNames(dao.dataSource, groupBys)...)
	return dao
}
func (dao SelectDao[T]) GroupByRow(groupBys ...string) SelectDao[T] {
	dao.builder = dao.builder.GroupBy(groupBys...)
	return dao
}

func (dao SelectDao[T]) OrderBy(field string) SelectDao[T] {
	if strings.Contains(field, " ") {
		panic(exception.New("order by 不能包含空格"))
	}
	dao.builder = dao.builder.OrderBy(dao.modelMeta.escapeName(dao.dataSource, field))
	return dao
}
func (dao SelectDao[T]) OrderByDesc(field string) SelectDao[T] {
	dao.builder = dao.builder.OrderBy(dao.modelMeta.escapeName(dao.dataSource, field) + " DESC")
	return dao
}

// orderByExprPattern 排序表达式的合法字符白名单：字母/数字/下划线/点/逗号/
// 空格/圆括号/连字符。引号、分号等注入载体一律拒绝。
var orderByExprPattern = regexp.MustCompile(`^[A-Za-z0-9_.,()\s-]+$`)

// OrderByExpr 按原始排序表达式排序，如 "(price - member_price) desc, id desc"。
// 表达式经字符白名单校验后原样拼入 ORDER BY（不经 escapeName 包裹），
// 因此可携带函数（random()）与多列多方向；禁止传入用户可控输入。
func (dao SelectDao[T]) OrderByExpr(expr string) SelectDao[T] {
	if !orderByExprPattern.MatchString(expr) {
		panic(exception.New("order by 表达式含非法字符"))
	}
	dao.builder = dao.builder.OrderBy(expr)
	return dao
}

func (dao SelectDao[T]) Limit(limit uint64) SelectDao[T] {
	dao.builder = dao.builder.Limit(limit)
	return dao
}

func (dao SelectDao[T]) Offset(offset uint64) SelectDao[T] {
	dao.builder = dao.builder.Offset(offset)
	return dao
}

// 重置select选项
func (dao SelectDao[T]) resetColumns(fields ...string) SelectDao[T] {
	dao.builder = dao.builder.RemoveColumns().Columns(fields...)
	return dao
}

func (dao SelectDao[T]) whereNLogicDel() SelectDao[T] {
	if dao.modelMeta.logicDelKey.Key != "" {
		logicDelKey := dao.modelMeta.logicDelKey.Key
		if dao.fromAs != "" {
			logicDelKey = dao.fromAs + "." + logicDelKey
		}
		return dao.Where(squirrel.NotEq{logicDelKey: dao.LogicDelVal[0]})
	}
	return dao
}

// 生成sql中: sth in (select unnest(Array[?,?,?])) []any
// 注意使用时 args...
func (dao SelectDao[T]) whereUnnest(arr any, key, flag string) SelectDao[T] {
	switch dao.dataSource.Driver {
	case sqlconst.Postgres, sqlconst.Kingbase:
		s, v := pgArray(arr)
		return dao.Where(fmt.Sprintf("%s %s (select unnest(%s))", dao.modelMeta.escapeName(dao.dataSource, key), flag, s), v...)
	default:
		s, v := normalArray(arr)
		return dao.Where(fmt.Sprintf("%s %s %s", dao.modelMeta.escapeName(dao.dataSource, key), flag, s), v...)
	}
}
func (dao SelectDao[T]) WhereUnnestIn(key string, arr any) SelectDao[T] {
	return dao.whereUnnest(arr, key, "IN")
}
func (dao SelectDao[T]) WhereUnnestNotIn(key string, arr any) SelectDao[T] {
	return dao.whereUnnest(arr, key, "NOT IN")
}

// WhereArrayIn 用于PG中array类型数据的包含比较
func (dao SelectDao[T]) WhereArrayIn(key string, arr any) SelectDao[T] {
	switch dao.dataSource.Driver {
	case sqlconst.Postgres, sqlconst.Kingbase:
		s, v := pgArray(arr)
		return dao.Where(fmt.Sprintf("%s @> %s", dao.modelMeta.escapeName(dao.dataSource, key), s), v...)
	default:
		panic(exception.New("WhereArrayIn not supported"))
	}
}
func (dao SelectDao[T]) WhereArrayNotIn(key string, arr any) SelectDao[T] {
	switch dao.dataSource.Driver {
	case sqlconst.Postgres, sqlconst.Kingbase:
		s, v := pgArray(arr)
		return dao.Where(fmt.Sprintf("not (%s @> %s)", dao.modelMeta.escapeName(dao.dataSource, key), s), v...)
	default:
		panic(exception.New("WhereArrayNotIn not supported"))
	}
}

func (dao SelectDao[T]) WhereIn(key string, sub SubQueryInterface) SelectDao[T] {
	sql, args := sub.sqlOriginPlaceholder()
	return dao.Where(squirrel.Expr(key+" IN ("+sql+")", args...))
}
func (dao SelectDao[T]) WhereLike(field string, val string) SelectDao[T] {
	return dao.Where(squirrel.Like{field: "%" + val + "%"})
}

// ============ S1: IgnoreLogicDel 的便捷终结方法 ============
// OneIgnoreDel 取一条并忽略逻辑删除过滤，等价于 dao.Select().Where(...).IgnoreLogicDel().One()
func (dao SelectDao[T]) OneIgnoreDel() *T {
	return dao.IgnoreLogicDel().One()
}
func (dao SelectDao[T]) ListIgnoreDel() []*T {
	return dao.IgnoreLogicDel().List()
}

// ============ S2: CTE / WITH 支持 ============
// With 增加 WITH 子句（非递归 CTE）。name 为 CTE 名，columns 为可选列名，sub 为子查询。
func (dao SelectDao[T]) With(name string, columns []string, sub SubQueryInterface) SelectDao[T] {
	sql, args := sub.sqlOriginPlaceholder()
	prefix := name
	if len(columns) > 0 {
		prefix += "(" + strings.Join(columns, ", ") + ")"
	}
	prefix += " AS (" + sql + ")"
	dao.builder = dao.builder.Prefix(prefix, args...)
	return dao
}

// WithRecursive 增加 WITH RECURSIVE 子句。用于递归 CTE（如树形结构查询）。
// sub 为 CTE 的查询体（含递归项）。调用方需在 sub 内自行组织 UNION/UNION ALL。
func (dao SelectDao[T]) WithRecursive(name string, columns []string, sub SubQueryInterface) SelectDao[T] {
	sql, args := sub.sqlOriginPlaceholder()
	prefix := "WITH RECURSIVE " + name
	if len(columns) > 0 {
		prefix += "(" + strings.Join(columns, ", ") + ")"
	}
	prefix += " AS (" + sql + ")"
	dao.builder = dao.builder.Prefix(prefix, args...)
	return dao
}

// WithRecursiveRaw 用原始 SQL 字符串作为递归 CTE 体。
// sqlBody 为完整 CTE 体（不含 name 与 AS），args 为其参数。
// 适用于不便构造 SubQueryInterface 的复杂递归查询。
func (dao SelectDao[T]) WithRecursiveRaw(name string, columns []string, sqlBody string, args ...any) SelectDao[T] {
	prefix := "WITH RECURSIVE " + name
	if len(columns) > 0 {
		prefix += "(" + strings.Join(columns, ", ") + ")"
	}
	prefix += " AS (" + sqlBody + ")"
	dao.builder = dao.builder.Prefix(prefix, args...)
	return dao
}

// ============ S3: jsonb 谓词（防注入）============
// WhereJsonbPathText 对 PG jsonb 字段做 text 路径比较：extend->>? = ?
// key 与值均参数化（此前 key 经 EscapeName 包裹成标识符 "key"，PG 上报
// column "key" does not exist，且标识符引号不清洗内嵌引号存在注入面）。
func (dao SelectDao[T]) WhereJsonbPathText(jsonbCol, key, op string, val any) SelectDao[T] {
	col := dao.modelMeta.escapeName(dao.dataSource, jsonbCol)
	return dao.Where(fmt.Sprintf("%s->>? %s ?", col, op), key, val)
}

// WhereJsonbPathEq extend->>'key' = val 的快捷写法
func (dao SelectDao[T]) WhereJsonbPathEq(jsonbCol, key string, val any) SelectDao[T] {
	return dao.WhereJsonbPathText(jsonbCol, key, "=", val)
}

// WhereJsonbContains PG jsonb @> 包含比较：col @> ?
func (dao SelectDao[T]) WhereJsonbContains(jsonbCol string, val any) SelectDao[T] {
	col := dao.modelMeta.escapeName(dao.dataSource, jsonbCol)
	return dao.Where(fmt.Sprintf("%s @> ?", col), val)
}
