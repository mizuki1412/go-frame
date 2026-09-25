package sqlkit

import (
	"context"
	"fmt"

	"github.com/Masterminds/squirrel"
	"github.com/example/go-frame/pkg/class/const/sqlconst"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/library/jsonkit"
	"github.com/example/go-frame/pkg/service/logkit"
)

type DeleteDao[T any] struct {
	Dao[T]
	builder squirrel.DeleteBuilder
}

func (dao DeleteDao[T]) Print() {
	sql, args := dao.Sql()
	logkit.Info("sql print", "sql", sql, "args", jsonkit.ToString(args))
}

func (dao DeleteDao[T]) Exec() int64 {
	return dao.ExecCtx(context.Background())
}

// ExecCtx 带 ctx 的执行，支持超时与取消传播。
func (dao DeleteDao[T]) ExecCtx(ctx context.Context) int64 {
	sql, args := dao.Sql()
	res := dao.ExecRawCtx(ctx, sql, args)
	rn, _ := res.RowsAffected()
	logkit.Debug("sql res", "rows", rn)
	return rn
}

// ExecRows 为 Exec 的语义化别名，返回受影响行数，避免与 sql.Result 混淆。
func (dao DeleteDao[T]) ExecRows() int64 {
	return dao.Exec()
}

func (dao DeleteDao[T]) Sql() (string, []any) {
	sqls, args, err := dao.ToSql()
	if err != nil {
		panic(exception.New(err.Error()))
	}
	return sqls, args
}
func (dao DeleteDao[T]) ToSql() (string, []any, error) {
	dao.builder = dao.builder.PlaceholderFormat(placeholder(dao.dataSource.Driver))
	sqls, args, err := dao.builder.ToSql()
	return sqls, argsWrap(dao.dataSource.Driver, args), err
}

// Prefix 在 sql 前写入语句
func (dao DeleteDao[T]) Prefix(sql string, args ...any) DeleteDao[T] {
	dao.builder = dao.builder.Prefix(sql, args...)
	return dao
}
func (dao DeleteDao[T]) PrefixExpr(expr squirrel.Sqlizer) DeleteDao[T] {
	dao.builder = dao.builder.PrefixExpr(expr)
	return dao
}
func (dao DeleteDao[T]) Suffix(sql string, args ...any) DeleteDao[T] {
	dao.builder = dao.builder.Suffix(sql, args...)
	return dao
}
func (dao DeleteDao[T]) SuffixExpr(expr squirrel.Sqlizer) DeleteDao[T] {
	dao.builder = dao.builder.SuffixExpr(expr)
	return dao
}

func (dao DeleteDao[T]) Where(pred any, args ...any) DeleteDao[T] {
	dao.builder = dao.builder.Where(handlePlaceholderInWhere(dao.dataSource.Driver, pred, args...), args...)
	return dao
}

// custom 参考dao_builder_select

func (dao DeleteDao[T]) whereUnnest(arr any, key, flag string) DeleteDao[T] {
	switch dao.dataSource.Driver {
	case sqlconst.Postgres, sqlconst.Kingbase:
		s, v := pgArray(arr)
		return dao.Where(fmt.Sprintf("%s %s (select unnest(%s))", dao.modelMeta.escapeName(dao.dataSource, key), flag, s), v...)
	default:
		panic(exception.New("whereUnnest not supported"))
	}
}
func (dao DeleteDao[T]) WhereUnnestIn(key string, arr any) DeleteDao[T] {
	return dao.whereUnnest(arr, key, "IN")
}
func (dao DeleteDao[T]) WhereUnnestNotIn(key string, arr any) DeleteDao[T] {
	return dao.whereUnnest(arr, key, "NOT IN")
}

// WhereArrayIn 用于PG中array类型数据的包含比较
func (dao DeleteDao[T]) WhereArrayIn(key string, arr any) DeleteDao[T] {
	switch dao.dataSource.Driver {
	case sqlconst.Postgres, sqlconst.Kingbase:
		s, v := pgArray(arr)
		return dao.Where(fmt.Sprintf("%s @> %s", dao.modelMeta.escapeName(dao.dataSource, key), s), v...)
	default:
		panic(exception.New("WhereArrayIn not supported"))
	}
}
func (dao DeleteDao[T]) WhereArrayNotIn(key string, arr any) DeleteDao[T] {
	switch dao.dataSource.Driver {
	case sqlconst.Postgres, sqlconst.Kingbase:
		s, v := pgArray(arr)
		return dao.Where(fmt.Sprintf("not (%s @> %s)", dao.modelMeta.escapeName(dao.dataSource, key), s), v...)
	default:
		panic(exception.New("WhereArrayNotIn not supported"))
	}
}

func (dao DeleteDao[T]) WhereIn(key string, sub SubQueryInterface) DeleteDao[T] {
	sql, args := sub.sqlOriginPlaceholder()
	return dao.Where(squirrel.Expr(key+" IN ("+sql+")", args...))
}
func (dao DeleteDao[T]) WhereLike(field string, val string) DeleteDao[T] {
	return dao.Where(squirrel.Like{field: "%" + val + "%"})
}

// ============ S3: jsonb 谓词（防注入）============
// key 与值均参数化，与 SelectDao.WhereJsonbPathText 保持一致。
func (dao DeleteDao[T]) WhereJsonbPathText(jsonbCol, key, op string, val any) DeleteDao[T] {
	col := dao.modelMeta.escapeName(dao.dataSource, jsonbCol)
	return dao.Where(fmt.Sprintf("%s->>? %s ?", col, op), key, val)
}
func (dao DeleteDao[T]) WhereJsonbPathEq(jsonbCol, key string, val any) DeleteDao[T] {
	return dao.WhereJsonbPathText(jsonbCol, key, "=", val)
}
func (dao DeleteDao[T]) WhereJsonbContains(jsonbCol string, val any) DeleteDao[T] {
	col := dao.modelMeta.escapeName(dao.dataSource, jsonbCol)
	return dao.Where(fmt.Sprintf("%s @> ?", col), val)
}
