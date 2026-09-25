package sqlkit

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/Masterminds/squirrel"
	"github.com/example/go-frame/pkg/class"
	"github.com/example/go-frame/pkg/class/const/sqlconst"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/service/logkit"
	"github.com/spf13/cast"
)

func (dao Dao[T]) InsertObj(dest *T) {
	dao.InsertObjCtx(context.Background(), dest)
}

// InsertObjCtx 带 ctx 的插入，支持超时与取消传播。
func (dao Dao[T]) InsertObjCtx(ctx context.Context, dest *T) {
	var columns []string
	var vals []any
	rv := reflect.ValueOf(dest).Elem()
	for _, e := range dao.modelMeta.allInsertKeys {
		var val = e.val(rv, dao.dataSource.Driver)
		if val == nil {
			continue
		}
		if sqlconst.IsTaos(dao.dataSource.Driver) {
			columns = append(columns, dao.dataSource.EscapeName(e.OriKey))
		} else {
			columns = append(columns, e.OriKey)
		}
		vals = append(vals, val)
	}
	if len(columns) == 0 {
		panic(exception.New("no fields", 2))
	}
	if sqlconst.IsTaos(dao.dataSource.Driver) {
		// 针对taos重写
		vals = argsWrap(dao.dataSource.Driver, vals)
		valPlaceholders := make([]string, 0, len(vals))
		for _, e := range vals {
			switch e.(type) {
			case string, class.String:
				valPlaceholders = append(valPlaceholders, "'?'")
			default:
				valPlaceholders = append(valPlaceholders, "?")
			}
		}
		ss := fmt.Sprintf("insert into %s(%s) values(%s)",
			dao.modelMeta.getTable(dao.dataSource), strings.Join(columns, ", "), strings.Join(valPlaceholders, ", "))
		res := dao.ExecRawCtx(ctx, ss, vals)
		rn, _ := res.RowsAffected()
		logkit.Debug("sql res", "rows", rn)
	} else if sqlconst.IsPostgresType(dao.dataSource.Driver) || dao.dataSource.Driver == sqlconst.Sqlite3 {
		// PG/Kingbase/SQLite 支持 RETURNING *，可一次拿到插入后的完整行（含自增主键）
		builder := dao.Insert()
		builder = builder.Columns(columns...).Values(vals...)
		builder = builder.Suffix("returning *")
		builder.ReturnOneCtx(ctx, dest)
	} else {
		// MySQL/SQL Server/Oracle/DM 等不支持 RETURNING *。
		// S8: 通过 LastInsertId 回填自增主键到 dest，与 PG 的 RETURNING * 行为对齐。
		builder := dao.Insert()
		builder = builder.Columns(columns...).Values(vals...)
		sql, args := builder.Sql()
		res := dao.ExecRawCtx(ctx, sql, args)
		if id, err := res.LastInsertId(); err == nil && id > 0 {
			rv := reflect.ValueOf(dest).Elem()
			for _, pk := range dao.modelMeta.allPKs {
				if pk.Auto {
					setAutoPK(rv, pk.RStruct.Name, id)
				}
			}
		}
	}
}

// InsertObjIgnoreConflict 插入并在唯一键冲突时静默跳过（P2：check-then-insert
// 竞态的兜底）。PG/Kingbase/SQLite 用 ON CONFLICT DO NOTHING；MySQL 系用
// INSERT IGNORE；其余驱动退化为普通插入。返回受影响行数（0=冲突跳过）。
// 调用方须保证表上存在对应唯一索引，否则冲突不生效。
func (dao Dao[T]) InsertObjIgnoreConflict(dest *T) int64 {
	return dao.InsertObjIgnoreConflictCtx(context.Background(), dest)
}

// InsertObjIgnoreConflictCtx 带 ctx 的 InsertObjIgnoreConflict。
func (dao Dao[T]) InsertObjIgnoreConflictCtx(ctx context.Context, dest *T) int64 {
	var columns []string
	var vals []any
	rv := reflect.ValueOf(dest).Elem()
	for _, e := range dao.modelMeta.allInsertKeys {
		var val = e.val(rv, dao.dataSource.Driver)
		if val == nil {
			continue
		}
		columns = append(columns, e.OriKey)
		vals = append(vals, val)
	}
	if len(columns) == 0 {
		panic(exception.New("no fields", 2))
	}
	builder := dao.Insert().Columns(columns...).Values(vals...)
	switch {
	case sqlconst.IsPostgresType(dao.dataSource.Driver) || dao.dataSource.Driver == sqlconst.Sqlite3:
		builder = builder.Suffix("on conflict do nothing")
		return builder.ExecCtx(ctx)
	case dao.dataSource.Driver == sqlconst.Mysql:
		// 手拼 SQL 的列名同样要转义，防保留字/特殊字符
		quoted := make([]string, len(columns))
		for i, c := range columns {
			quoted[i] = dao.dataSource.EscapeName(c)
		}
		res := dao.ExecRawCtx(ctx, "insert ignore into "+dao.modelMeta.getTable(dao.dataSource)+
			"("+strings.Join(quoted, ", ")+") values("+
			strings.TrimSuffix(strings.Repeat("?, ", len(vals)), ", ")+")", vals)
		rn, _ := res.RowsAffected()
		return rn
	default:
		return builder.ExecCtx(ctx)
	}
}

// UpsertObj 插入，唯一键冲突时改为更新（须保证表上有对应唯一索引）。
// PG/Kingbase/SQLite 走 ON CONFLICT (conflictCols...) DO UPDATE；MySQL 走
// ON DUPLICATE KEY UPDATE；其余驱动不支持，直接 panic。更新列为除主键/自增与
// 冲突列之外的全部可更新列（与 UpdateObj 的列口径一致）。
func (dao Dao[T]) UpsertObj(dest *T, conflictCols ...string) int64 {
	return dao.UpsertObjCtx(context.Background(), dest, conflictCols...)
}

// UpsertObjCtx 带 ctx 的 UpsertObj。
func (dao Dao[T]) UpsertObjCtx(ctx context.Context, dest *T, conflictCols ...string) int64 {
	builder := dao.buildUpsert(dest, conflictCols...)
	return builder.ExecCtx(ctx)
}

// buildUpsert 构造 upsert 语句（insert ... on conflict/do duplicate key ...），
// 与执行分离以便对生成的 SQL 做单测。
func (dao Dao[T]) buildUpsert(dest *T, conflictCols ...string) InsertDao[T] {
	if len(conflictCols) == 0 {
		panic(exception.New("UpsertObj 需要至少一个冲突列"))
	}
	var columns []string
	var vals []any
	rv := reflect.ValueOf(dest).Elem()
	for _, e := range dao.modelMeta.allInsertKeys {
		val := e.val(rv, dao.dataSource.Driver)
		if val == nil {
			continue
		}
		columns = append(columns, e.OriKey)
		vals = append(vals, val)
	}
	if len(columns) == 0 {
		panic(exception.New("no fields", 2))
	}
	conflict := make(map[string]bool, len(conflictCols))
	for _, c := range conflictCols {
		conflict[c] = true
	}
	builder := dao.Insert().Columns(columns...).Values(vals...)
	// 更新列 = 可更新列（含逻辑删除，与 UpdateObj 口径一致）去掉冲突列
	var upKeys []ModelMetaKey
	for _, e := range dao.modelMeta.allUpdateKeys {
		if conflict[e.OriKey] {
			continue
		}
		upKeys = append(upKeys, e)
	}
	if len(upKeys) == 0 {
		panic(exception.New("UpsertObj 更新列为空：冲突列覆盖了全部可更新列"))
	}
	switch {
	case sqlconst.IsPostgresType(dao.dataSource.Driver) || dao.dataSource.Driver == sqlconst.Sqlite3:
		setPairs := make([]string, len(upKeys))
		for i, e := range upKeys {
			setPairs[i] = e.Key + "=excluded." + e.Key
		}
		quoted := dao.modelMeta.escapeNames(dao.dataSource, conflictCols)
		builder = builder.Suffix("on conflict ("+strings.Join(quoted, ", ")+") do update set "+strings.Join(setPairs, ", "))
	case dao.dataSource.Driver == sqlconst.Mysql:
		setPairs := make([]string, len(upKeys))
		for i, e := range upKeys {
			setPairs[i] = e.Key + "=values(" + e.Key + ")"
		}
		builder = builder.Suffix("on duplicate key update " + strings.Join(setPairs, ", "))
	default:
		panic(exception.New("UpsertObj not supported on driver " + dao.dataSource.Driver))
	}
	return builder
}

// setAutoPK 将自增主键回填到 model 的 class.Int64 / sql.NullInt64 / 原生整数字段。
func setAutoPK(rv reflect.Value, fieldName string, id int64) {
	f := rv.FieldByName(fieldName)
	if !f.IsValid() || !f.CanSet() {
		return
	}
	// 优先用 Set(any)（class.Int64 等实现）
	if setter, ok := f.Addr().Interface().(interface{ Set(any) }); ok {
		setter.Set(id)
		return
	}
	switch f.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		f.SetInt(id)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		f.SetUint(uint64(id))
	}
}

func (dao Dao[T]) InsertBatch(dest []*T) {
	dao.InsertBatchCtx(context.Background(), dest)
}

// InsertBatchCtx 带 ctx 的批量插入，支持超时与取消传播。
func (dao Dao[T]) InsertBatchCtx(ctx context.Context, dest []*T) {
	// S15: 空切片直接返回，不再 panic（批量导入场景常见空入参）
	if len(dest) == 0 {
		return
	}
	insertKeys := dao.modelMeta.allInsertKeys
	// 单遍收集：每行按 allInsertKeys 顺序取一次值，同时汇总非空列并集，
	// 保证每行 vals 与 columns 严格对齐（此前取值两遍；某行字段为 nil 被
	// 跳过、其他行该字段有值时若不对齐会导致列与值错位）。
	captured := make([][]any, len(dest))
	colKeys := make([]ModelMetaKey, 0)
	colIndex := make(map[string]int)
	for i, e := range dest {
		rv := reflect.ValueOf(e).Elem()
		row := make([]any, len(insertKeys))
		for j := range insertKeys {
			k := &insertKeys[j]
			val := k.val(rv, dao.dataSource.Driver)
			if val == nil {
				continue
			}
			row[j] = val
			if _, ok := colIndex[k.OriKey]; !ok {
				colIndex[k.OriKey] = len(colKeys)
				colKeys = append(colKeys, *k)
			}
		}
		captured[i] = row
	}
	if len(colKeys) == 0 {
		panic(exception.New("no fields", 2))
	}
	// 按并集对齐每行 vals，缺失字段补 nil
	valsArr := make([][]any, 0, len(captured))
	for _, row := range captured {
		out := make([]any, len(colKeys))
		for j := range insertKeys {
			if row[j] == nil {
				continue
			}
			out[colIndex[insertKeys[j].OriKey]] = row[j]
		}
		valsArr = append(valsArr, out)
	}
	if sqlconst.IsTaos(dao.dataSource.Driver) {
		var columns []string
		for _, k := range colKeys {
			columns = append(columns, dao.dataSource.EscapeName(k.OriKey))
		}
		sql := ""
		var allVals []any
		for i := 0; i < len(valsArr); i++ {
			vals := argsWrap(dao.dataSource.Driver, valsArr[i])
			valPlaceholders := make([]string, 0, len(vals))
			for _, e := range vals {
				switch e.(type) {
				case string, class.String:
					valPlaceholders = append(valPlaceholders, "'?'")
				default:
					valPlaceholders = append(valPlaceholders, "?")
				}
			}
			if i == 0 {
				sql += fmt.Sprintf("insert into %s(%s) values", dao.modelMeta.getTable(dao.dataSource), strings.Join(columns, ", "))
			}
			sql += fmt.Sprintf("(%s)", strings.Join(valPlaceholders, ", "))
			if i < len(valsArr)-1 {
				sql += ", "
			}
			allVals = append(allVals, vals...)
		}
		res := dao.ExecRawCtx(ctx, sql, allVals)
		rn, _ := res.RowsAffected()
		logkit.Debug("sql res", "rows", rn)
	} else {
		var columns []string
		for _, k := range colKeys {
			columns = append(columns, k.OriKey)
		}
		// 分片执行：单语句参数超限（PG 协议 65535 上限、MySQL max_allowed_packet）
		// 会直接报错，按列数推导单语句行数上限；同一事务/连接内多条执行。
		chunk := insertBatchChunkSize(len(columns))
		for start := 0; start < len(valsArr); start += chunk {
			end := start + chunk
			if end > len(valsArr) {
				end = len(valsArr)
			}
			builder := dao.Insert()
			builder = builder.Columns(columns...)
			for i := start; i < end; i++ {
				builder = builder.Values(valsArr[i]...)
			}
			builder.ExecCtx(ctx)
		}
	}
}

func (dao Dao[T]) UpdateObj(dest *T) int64 {
	return dao.UpdateObjCtx(context.Background(), dest)
}

// UpdateObjCtx 带 ctx 的更新，支持超时与取消传播。
func (dao Dao[T]) UpdateObjCtx(ctx context.Context, dest *T) int64 {
	builder := dao.Update()
	rv := reflect.ValueOf(dest).Elem()
	for _, e := range dao.modelMeta.allUpdateKeys {
		var val = e.val(rv, dao.dataSource.Driver)
		if val == nil {
			continue
		}
		// 针对class.MapString 采用merge方式 todo mysql
		// MapStringSync 自 P0 修复后仅支持指针字段形式（值形式会拷贝内嵌锁），两种写法都识别
		if (e.RStruct.Type.String() == "class.MapString" || e.RStruct.Type.String() == "class.MapStringSync" || e.RStruct.Type.String() == "*class.MapStringSync") && sqlconst.IsPostgresType(dao.dataSource.Driver) {
			builder = builder.Set(e.OriKey, squirrel.Expr("coalesce("+e.OriKey+",'{}'::jsonb) || ?", val))
		} else {
			builder = builder.Set(e.OriKey, val)
		}
	}
	for _, e := range dao.modelMeta.allPKs {
		v := e.val(rv, dao.dataSource.Driver)
		if v == nil {
			panic(exception.New("pk val is nil"))
		}
		builder = builder.Where(e.Key+"=?", v)
	}
	return builder.ExecCtx(ctx)
}

func (dao Dao[T]) DeleteById(id ...any) int64 {
	return dao.DeleteByIdCtx(context.Background(), id...)
}

// DeleteByIdCtx 带 ctx 的按主键删除，支持超时与取消传播。
func (dao Dao[T]) DeleteByIdCtx(ctx context.Context, id ...any) int64 {
	if len(id) != len(dao.modelMeta.allPKs) {
		panic(exception.New("主键数量不匹配"))
	}
	if dao.modelMeta.logicDelKey.Key != "" {
		builder := dao.Update()
		builder = builder.Set(dao.modelMeta.logicDelKey.OriKey, builder.LogicDelVal[0])
		for i := 0; i < len(dao.modelMeta.allPKs); i++ {
			builder = builder.Where(dao.modelMeta.allPKs[i].Key+"=?", id[i])
		}
		return builder.ExecCtx(ctx)
	} else {
		builder := dao.Delete()
		for i := 0; i < len(dao.modelMeta.allPKs); i++ {
			builder = builder.Where(dao.modelMeta.allPKs[i].Key+"=?", id[i])
		}
		return builder.ExecCtx(ctx)
	}
}

// SelectOneById 根据id获取，计算逻辑删除
func (dao Dao[T]) SelectOneById(id ...any) *T {
	return dao.SelectOneByIdCtx(context.Background(), id...)
}

// SelectOneByIdCtx 带 ctx 的 SelectOneById。
func (dao Dao[T]) SelectOneByIdCtx(ctx context.Context, id ...any) *T {
	builder := dao.Select()
	if len(id) != len(dao.modelMeta.allPKs) {
		panic(exception.New("主键数量不匹配"))
	}
	for i := 0; i < len(dao.modelMeta.allPKs); i++ {
		builder = builder.Where(dao.modelMeta.allPKs[i].Key+"=?", id[i])
	}
	return builder.OneCtx(ctx)
}

// SelectOneWithDelById 根据id获取，忽略逻辑删除
func (dao Dao[T]) SelectOneWithDelById(id ...any) *T {
	return dao.SelectOneWithDelByIdCtx(context.Background(), id...)
}

// SelectOneWithDelByIdCtx 带 ctx 的 SelectOneWithDelById。
func (dao Dao[T]) SelectOneWithDelByIdCtx(ctx context.Context, id ...any) *T {
	builder := dao.Select()
	if len(id) != len(dao.modelMeta.allPKs) {
		panic(exception.New("主键数量不匹配"))
	}
	for i := 0; i < len(dao.modelMeta.allPKs); i++ {
		builder = builder.Where(dao.modelMeta.allPKs[i].Key+"=?", id[i])
	}
	return builder.IgnoreLogicDel().OneCtx(ctx)
}

// SelectByIdsIgnoreDel S12: 根据id列表批量获取，忽略逻辑删除。
// 仅支持单主键表。用于级联批量查询场景，避免 N+1。
// ids 为空时返回 nil，不发 SQL。
func (dao Dao[T]) SelectByIdsIgnoreDel(ids []int64) []*T {
	return dao.SelectByIdsIgnoreDelCtx(context.Background(), ids)
}

// SelectByIdsIgnoreDelCtx 带 ctx 的 SelectByIdsIgnoreDel。
func (dao Dao[T]) SelectByIdsIgnoreDelCtx(ctx context.Context, ids []int64) []*T {
	if len(ids) == 0 {
		return nil
	}
	if len(dao.modelMeta.allPKs) != 1 {
		panic(exception.New("SelectByIdsIgnoreDel 仅支持单主键表"))
	}
	return dao.Select().WhereUnnestIn(dao.modelMeta.allPKs[0].OriKey, ids).IgnoreLogicDel().ListCtx(ctx)
}

// SelectByIds S12: 根据id列表批量获取，计算逻辑删除。
// 仅支持单主键表。ids 为空时返回 nil。
func (dao Dao[T]) SelectByIds(ids []int64) []*T {
	return dao.SelectByIdsCtx(context.Background(), ids)
}

// SelectByIdsCtx 带 ctx 的 SelectByIds。
func (dao Dao[T]) SelectByIdsCtx(ctx context.Context, ids []int64) []*T {
	if len(ids) == 0 {
		return nil
	}
	if len(dao.modelMeta.allPKs) != 1 {
		panic(exception.New("SelectByIds 仅支持单主键表"))
	}
	return dao.Select().WhereUnnestIn(dao.modelMeta.allPKs[0].OriKey, ids).ListCtx(ctx)
}

func (dao Dao[T]) CheckSchemaExist(schema string) bool {
	p1 := rawPlaceholder(dao.dataSource.Driver, 1)
	var sql string
	switch dao.dataSource.Driver {
	case sqlconst.Postgres, sqlconst.Kingbase:
		sql = "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname = " + p1 + ")"
	case sqlconst.Mysql:
		sql = "SELECT COUNT(1)>0 FROM information_schema.schemata WHERE schema_name = " + p1
	case sqlconst.Sqlite3:
		// SQLite 无 schema 概念，始终返回 true
		return true
	default:
		// 未实现的 driver，保守返回 true 不阻断流程
		return true
	}
	rows := dao.QueryRaw(sql, []any{schema})
	defer rows.Close()
	for rows.Next() {
		ret, err := rows.SliceScan()
		if err != nil {
			panic(exception.New(err.Error()))
		}
		return len(ret) > 0 && cast.ToBool(ret[0])
	}
	return false
}

func (dao Dao[T]) CheckTableExist(t string) bool {
	if dao.dataSource.Driver == sqlconst.Sqlite3 {
		rows := dao.QueryRaw(
			"SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?",
			[]any{t})
		defer rows.Close()
		for rows.Next() {
			ret, err := rows.SliceScan()
			if err != nil {
				panic(exception.New(err.Error()))
			}
			return len(ret) > 0 && cast.ToInt32(ret[0]) >= 1
		}
	} else {
		// todo other db
		panic(exception.New("CheckTableExist在此数据库未实现"))
	}
	return false
}
