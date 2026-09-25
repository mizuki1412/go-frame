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

type UpdateDao[T any] struct {
	Dao[T]
	builder squirrel.UpdateBuilder
}

func (dao UpdateDao[T]) Print() {
	sql, args := dao.Sql()
	logkit.Info("sql print", "sql", sql, "args", jsonkit.ToString(args))
}

func (dao UpdateDao[T]) Exec() int64 {
	return dao.ExecCtx(context.Background())
}

// ExecCtx 带 ctx 的执行，支持超时与取消传播。
func (dao UpdateDao[T]) ExecCtx(ctx context.Context) int64 {
	sql, args := dao.Sql()
	res := dao.ExecRawCtx(ctx, sql, args)
	rn, _ := res.RowsAffected()
	logkit.Debug("sql res", "rows", rn)
	return rn
}

// ExecRows 为 Exec 的语义化别名，返回受影响行数，避免与 sql.Result 混淆。
func (dao UpdateDao[T]) ExecRows() int64 {
	return dao.Exec()
}

func (dao UpdateDao[T]) Sql() (string, []any) {
	sqls, args, err := dao.ToSql()
	if err != nil {
		panic(exception.New(err.Error()))
	}
	return sqls, args
}
func (dao UpdateDao[T]) ToSql() (string, []any, error) {
	dao.builder = dao.builder.PlaceholderFormat(placeholder(dao.dataSource.Driver))
	sqls, args, err := dao.builder.ToSql()
	return sqls, argsWrap(dao.dataSource.Driver, args), err
}

// Prefix 在 sql 前写入语句
func (dao UpdateDao[T]) Prefix(sql string, args ...interface{}) UpdateDao[T] {
	dao.builder = dao.builder.Prefix(sql, args...)
	return dao
}
func (dao UpdateDao[T]) PrefixExpr(expr squirrel.Sqlizer) UpdateDao[T] {
	dao.builder = dao.builder.PrefixExpr(expr)
	return dao
}
func (dao UpdateDao[T]) Suffix(sql string, args ...interface{}) UpdateDao[T] {
	dao.builder = dao.builder.Suffix(sql, args...)
	return dao
}
func (dao UpdateDao[T]) SuffixExpr(expr squirrel.Sqlizer) UpdateDao[T] {
	dao.builder = dao.builder.SuffixExpr(expr)
	return dao
}

func (dao UpdateDao[T]) Set(column string, value interface{}) UpdateDao[T] {
	dao.builder = dao.builder.Set(dao.modelMeta.escapeName(dao.dataSource, column), value)
	return dao
}
func (dao UpdateDao[T]) Where(pred interface{}, args ...interface{}) UpdateDao[T] {
	dao.builder = dao.builder.Where(handlePlaceholderInWhere(dao.dataSource.Driver, pred, args...), args...)
	return dao
}
func (dao UpdateDao[T]) FromSelect(from SelectDao[T], alias string) UpdateDao[T] {
	dao.builder = dao.builder.FromSelect(from.builder, alias)
	return dao
}

// custom 参考dao_builder_select

func (dao UpdateDao[T]) whereUnnest(arr any, key, flag string) UpdateDao[T] {
	switch dao.dataSource.Driver {
	case sqlconst.Postgres, sqlconst.Kingbase:
		s, v := pgArray(arr)
		return dao.Where(fmt.Sprintf("%s %s (select unnest(%s))", dao.modelMeta.escapeName(dao.dataSource, key), flag, s), v...)
	default:
		panic(exception.New("whereUnnest not supported"))
	}
}
func (dao UpdateDao[T]) WhereUnnestIn(key string, arr any) UpdateDao[T] {
	return dao.whereUnnest(arr, key, "IN")
}
func (dao UpdateDao[T]) WhereUnnestNotIn(key string, arr any) UpdateDao[T] {
	return dao.whereUnnest(arr, key, "NOT IN")
}

// WhereArrayIn 用于PG中array类型数据的包含比较
func (dao UpdateDao[T]) WhereArrayIn(key string, arr any) UpdateDao[T] {
	switch dao.dataSource.Driver {
	case sqlconst.Postgres, sqlconst.Kingbase:
		s, v := pgArray(arr)
		return dao.Where(fmt.Sprintf("%s @> %s", dao.modelMeta.escapeName(dao.dataSource, key), s), v...)
	default:
		panic(exception.New("WhereArrayIn not supported"))
	}
}
func (dao UpdateDao[T]) WhereArrayNotIn(key string, arr any) UpdateDao[T] {
	switch dao.dataSource.Driver {
	case sqlconst.Postgres, sqlconst.Kingbase:
		s, v := pgArray(arr)
		return dao.Where(fmt.Sprintf("not (%s @> %s)", dao.modelMeta.escapeName(dao.dataSource, key), s), v...)
	default:
		panic(exception.New("WhereArrayNotIn not supported"))
	}
}

func (dao UpdateDao[T]) WhereIn(key string, sub SubQueryInterface) UpdateDao[T] {
	sql, args := sub.sqlOriginPlaceholder()
	return dao.Where(squirrel.Expr(key+" IN ("+sql+")", args...))
}
func (dao UpdateDao[T]) WhereLike(field string, val string) UpdateDao[T] {
	return dao.Where(squirrel.Like{field: "%" + val + "%"})
}

// ============ S3: jsonb 谓词（防注入）============
// key 与值均参数化，与 SelectDao.WhereJsonbPathText 保持一致。
func (dao UpdateDao[T]) WhereJsonbPathText(jsonbCol, key, op string, val any) UpdateDao[T] {
	col := dao.modelMeta.escapeName(dao.dataSource, jsonbCol)
	return dao.Where(fmt.Sprintf("%s->>? %s ?", col, op), key, val)
}
func (dao UpdateDao[T]) WhereJsonbPathEq(jsonbCol, key string, val any) UpdateDao[T] {
	return dao.WhereJsonbPathText(jsonbCol, key, "=", val)
}
func (dao UpdateDao[T]) WhereJsonbContains(jsonbCol string, val any) UpdateDao[T] {
	col := dao.modelMeta.escapeName(dao.dataSource, jsonbCol)
	return dao.Where(fmt.Sprintf("%s @> ?", col), val)
}
