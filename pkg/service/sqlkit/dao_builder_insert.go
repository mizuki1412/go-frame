package sqlkit

import (
	"context"

	"github.com/Masterminds/squirrel"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/library/jsonkit"
	"github.com/example/go-frame/pkg/service/logkit"
)

type InsertDao[T any] struct {
	Dao[T]
	builder squirrel.InsertBuilder
}

func (dao InsertDao[T]) Print() {
	sql, args := dao.Sql()
	logkit.Info("sql print", "sql", sql, "args", jsonkit.ToString(args))
}

func (dao InsertDao[T]) Exec() int64 {
	return dao.ExecCtx(context.Background())
}

// ExecCtx 带 ctx 的执行，支持超时与取消传播。
func (dao InsertDao[T]) ExecCtx(ctx context.Context) int64 {
	sql, args := dao.Sql()
	res := dao.ExecRawCtx(ctx, sql, args)
	rn, _ := res.RowsAffected()
	logkit.Debug("sql res", "rows", rn)
	return rn
}

// ExecRows 为 Exec 的语义化别名，返回受影响行数，避免与 sql.Result 混淆。
func (dao InsertDao[T]) ExecRows() int64 {
	return dao.Exec()
}

func (dao InsertDao[T]) ReturnOne(dest *T) {
	dao.ReturnOneCtx(context.Background(), dest)
}

// ReturnOneCtx 带 ctx 的 RETURNING 扫描，支持超时与取消传播。
func (dao InsertDao[T]) ReturnOneCtx(ctx context.Context, dest *T) {
	sql, args := dao.Sql()
	rows := dao.QueryRawCtx(ctx, sql, args)
	defer rows.Close()
	for rows.Next() {
		// return 赋值
		err := rows.StructScan(dest)
		if err != nil {
			panic(exception.New(err.Error()))
		}
		break
	}
	if err := rows.Err(); err != nil {
		panic(exception.New(err.Error()))
	}
}

func (dao InsertDao[T]) Sql() (string, []any) {
	sqls, args, err := dao.ToSql()
	if err != nil {
		panic(exception.New(err.Error()))
	}
	return sqls, args
}
func (dao InsertDao[T]) ToSql() (string, []any, error) {
	dao.builder = dao.builder.PlaceholderFormat(placeholder(dao.dataSource.Driver))
	sqls, args, err := dao.builder.ToSql()
	return sqls, argsWrap(dao.dataSource.Driver, args), err
}

// Prefix 在 sql 前写入语句
func (dao InsertDao[T]) Prefix(sql string, args ...any) InsertDao[T] {
	dao.builder = dao.builder.Prefix(sql, args...)
	return dao
}
func (dao InsertDao[T]) PrefixExpr(expr squirrel.Sqlizer) InsertDao[T] {
	dao.builder = dao.builder.PrefixExpr(expr)
	return dao
}
func (dao InsertDao[T]) Suffix(sql string, args ...any) InsertDao[T] {
	dao.builder = dao.builder.Suffix(sql, args...)
	return dao
}
func (dao InsertDao[T]) SuffixExpr(expr squirrel.Sqlizer) InsertDao[T] {
	dao.builder = dao.builder.SuffixExpr(expr)
	return dao
}

func (dao InsertDao[T]) Options(options ...string) InsertDao[T] {
	dao.builder = dao.builder.Options(options...)
	return dao
}

// Columns adds insert columns to the query.
func (dao InsertDao[T]) Columns(columns ...string) InsertDao[T] {
	dao.builder = dao.builder.Columns(dao.modelMeta.escapeNames(dao.dataSource, columns)...)
	return dao
}

// Values adds a single row's values to the query.
func (dao InsertDao[T]) Values(values ...any) InsertDao[T] {
	dao.builder = dao.builder.Values(values...)
	return dao
}

// Select 以子查询为插入源（insert into t select ...）。子查询先 applyFrom
// 保证自带 FROM 子句（此前直接嵌入 builder，生成的子查询没有 FROM，SQL 非法）。
func (dao InsertDao[T]) Select(sb SelectDao[T]) InsertDao[T] {
	sb = sb.applyFrom()
	dao.builder = dao.builder.Select(sb.builder)
	return dao
}
