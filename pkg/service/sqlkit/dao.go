package sqlkit

import (
	"context"
	"database/sql"

	"github.com/example/go-frame/pkg/class/const/sqlconst"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/library/jsonkit"
	"github.com/example/go-frame/pkg/service/logkit"
	"github.com/jmoiron/sqlx"
)

// LogicDelVal 全局逻辑删除的 value
var LogicDelVal = []any{true, false}

type Dao[T any] struct {
	meta T
	// 逻辑删除的字段，可替代全局的LogicDelVal
	LogicDelVal []any
	// 级联实现的函数（单条，向后兼容）
	Cascade func(*T)
	// 批量级联实现的函数。优先于 Cascade 使用，避免 N+1 查询
	CascadeBatch func([]*T)
	dataSource   *DataSource
	modelMeta    ModelMeta
}

type DaoModelMeta interface {
	getModelMeta() ModelMeta
	// S20: Join 等方法需要拿到目标 dao 的数据源以构造带 schema 的表名
	getDataSource() *DataSource
}

// New 必须从初始化函数生成 dao
func New[T any](ds ...*DataSource) Dao[T] {
	dao := Dao[T]{}
	if len(ds) > 0 {
		dao.dataSource = ds[0]
	} else {
		dao.dataSource = DefaultDataSource()
	}
	dao.modelMeta = dao.modelMeta.init(dao.meta, dao.dataSource)
	return dao
}

func (dao Dao[T]) getModelMeta() ModelMeta {
	return dao.modelMeta
}

// S20: 暴露数据源，供 Join 等方法构造目标表名
func (dao Dao[T]) getDataSource() *DataSource {
	return dao.dataSource
}

func (dao Dao[T]) DataSource() *DataSource {
	return dao.dataSource
}

func (dao Dao[T]) QueryRaw(sql string, args []any) *sqlx.Rows {
	return dao.QueryRawCtx(context.Background(), sql, args)
}

// QueryRawCtx 带 ctx 的原始查询，支持超时与取消传播。
func (dao Dao[T]) QueryRawCtx(ctx context.Context, sql string, args []any) *sqlx.Rows {
	// args 的 JSON 序列化较贵，级别不够时短路
	if logkit.DebugEnabled() {
		logkit.Debug("sql req", "sql", sql, "args", jsonkit.ToString(args))
	}
	return dao.dataSource.QueryCtx(ctx, sql, args)
}

// QueryRawRows 默认返回 T list，可用于自由sql时，自定义返回值
func (dao Dao[T]) QueryRawRows(sql string, args []any) []*T {
	return dao.QueryRawRowsCtx(context.Background(), sql, args)
}

// QueryRawRowsCtx 带 ctx 的 QueryRawRows。
func (dao Dao[T]) QueryRawRowsCtx(ctx context.Context, sql string, args []any) []*T {
	rows := dao.QueryRawCtx(ctx, sql, args)
	list := make([]*T, 0, 5)
	defer rows.Close()
	for rows.Next() {
		list = append(list, scanStruct[T](rows, dao.dataSource.Driver, dao.modelMeta))
	}
	if err := rows.Err(); err != nil {
		panic(exception.New(err.Error()))
	}
	cascadeList(dao, list)
	return list
}

func (dao Dao[T]) ExecRaw(sql string, args []any) sql.Result {
	return dao.ExecRawCtx(context.Background(), sql, args)
}

// ExecRawCtx 带 ctx 的原始执行，支持超时与取消传播。
func (dao Dao[T]) ExecRawCtx(ctx context.Context, sql string, args []any) sql.Result {
	if logkit.DebugEnabled() {
		logkit.Debug("sql exec", "sql", sql, "args", jsonkit.ToString(args))
	}
	return dao.dataSource.ExecCtx(ctx, sql, args)
}

func (dao Dao[T]) GetOriginDB() *sqlx.DB {
	return dao.dataSource.DBPool
}

// Close 程序退出时的逻辑
func (dao Dao[T]) Close() {
	if dao.dataSource.Driver == sqlconst.Sqlite3 {
		dao.GetOriginDB().Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	}
	dao.GetOriginDB().Close()
}

/// 小功能

func (dao Dao[T]) Table(alias ...string) string {
	return dao.modelMeta.getTable(dao.dataSource, alias...)
}
func (dao Dao[T]) EscapeNames(name ...string) []string {
	return dao.modelMeta.escapeNames(dao.dataSource, name)
}

// WithSchema S9: 返回一个使用指定 schema 的新 Dao，复用原 ModelMeta 缓存与 DBPool。
// 用于请求级 schema 注入，替代业务层反复 `dao.DataSource().Schema = ...`。
func (dao Dao[T]) WithSchema(schema string) Dao[T] {
	dao.dataSource = dao.dataSource.WithSchema(schema)
	return dao
}

// ============ S10/S11: Cascade 级联策略抽象 ============
// CascadeCtx 提供级联执行的上下文，供 CascadeFunc 决定是否执行某级联。
// S11: 替代单一 func(*T)，支持按需控制级联粒度。
type CascadeCtx struct {
	// Opts 为调用方传入的级联选项（由业务自定义）
	Opts any
	// Ds 为当前数据源，级联查询可用
	Ds *DataSource
}

// CascadeFunc 是带上下文的级联函数签名。
type CascadeFunc[T any] func(obj *T, ctx CascadeCtx)

// WithCascade S10: 替换 dao 的级联函数，支持 CascadeCtx 传参。
// 与原 Cascade 字段并存，当 WithCascade 设置后，List/One 执行级联时会优先使用它。
func (dao Dao[T]) WithCascade(f CascadeFunc[T]) Dao[T] {
	dao.Cascade = func(obj *T) {
		f(obj, CascadeCtx{Ds: dao.dataSource})
	}
	return dao
}

// WithCascadeOpts S11: 带业务 opts 的级联设置。
// opts 会随 CascadeCtx.Opts 传入级联函数，调用方可在 opts 中编码"只取 Role 不取 Department"等策略。
func (dao Dao[T]) WithCascadeOpts(opts any, f CascadeFunc[T]) Dao[T] {
	dao.Cascade = func(obj *T) {
		f(obj, CascadeCtx{Opts: opts, Ds: dao.dataSource})
	}
	return dao
}

// CascadeBatchFunc 批量级联函数签名。
// 一次性接收整个 list，调用方可在回调内收集所有 id 用 WHERE IN 单次查询后按 id 分发，
// 避免 N+1 查询。
type CascadeBatchFunc[T any] func(list []*T, ctx CascadeCtx)

// WithCascadeBatch S12: 替换 dao 的批量级联函数，优先于 WithCascade 设置的 Cascade 使用。
// List/QueryRawRows 在 list 长度>1 时走批量路径；长度==1 时若同时设置了 Cascade 则走单条，
// 否则仍走批量（批量函数内部自行处理单元素列表）。
func (dao Dao[T]) WithCascadeBatch(f CascadeBatchFunc[T]) Dao[T] {
	dao.CascadeBatch = func(list []*T) {
		f(list, CascadeCtx{Ds: dao.dataSource})
	}
	return dao
}

// WithCascadeBatchOpts S12: 带业务 opts 的批量级联设置。
// opts 会随 CascadeCtx.Opts 传入级联函数。
func (dao Dao[T]) WithCascadeBatchOpts(opts any, f CascadeBatchFunc[T]) Dao[T] {
	dao.CascadeBatch = func(list []*T) {
		f(list, CascadeCtx{Opts: opts, Ds: dao.dataSource})
	}
	return dao
}

// cascadeList 统一执行级联策略：设置了 CascadeBatch 时优先走批量（含单条 list，
// 仅当同时设置了 Cascade 才回退单条）；否则回退到逐条 Cascade。
// One 路径不应调用此函数。
func cascadeList[T any](dao Dao[T], list []*T) {
	if len(list) == 0 {
		return
	}
	if dao.CascadeBatch != nil && (len(list) > 1 || dao.Cascade == nil) {
		dao.CascadeBatch(list)
		return
	}
	if dao.Cascade != nil {
		for i := range list {
			dao.Cascade(list[i])
		}
	}
}
