package sqlkit

import (
	"context"
	"strings"
	"time"

	"github.com/example/go-frame/pkg/class/const/sqlconst"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/library/timekit"
	"github.com/spf13/cast"
)

/**
* 用于简单场景的sql，无规定model
 */

func (ds *DataSource) QueryOne(sql string, args ...any) any {
	return ds.QueryOneCtx(context.Background(), sql, args...)
}

func (ds *DataSource) QueryOneCtx(ctx context.Context, sql string, args ...any) any {
	rows := ds.QueryCtx(ctx, sql, args)
	defer rows.Close()
	for rows.Next() {
		ret, err := rows.SliceScan()
		if err != nil {
			panic(exception.New(err.Error()))
		}
		return ret[0]
	}
	if err := rows.Err(); err != nil {
		panic(exception.New(err.Error()))
	}
	return 0
}

func (ds *DataSource) QueryOneNumber(sql string, args ...any) int64 {
	return ds.QueryOneNumberCtx(context.Background(), sql, args...)
}

func (ds *DataSource) QueryOneNumberCtx(ctx context.Context, sql string, args ...any) int64 {
	rows := ds.QueryCtx(ctx, sql, args)
	defer rows.Close()
	for rows.Next() {
		ret, err := rows.SliceScan()
		if err != nil {
			panic(exception.New(err.Error()))
		}
		return cast.ToInt64(ret[0])
	}
	if err := rows.Err(); err != nil {
		panic(exception.New(err.Error()))
	}
	return 0
}

func (ds *DataSource) QueryOneMap(sql string, args ...any) map[string]any {
	return ds.QueryOneMapCtx(context.Background(), sql, args...)
}

func (ds *DataSource) QueryOneMapCtx(ctx context.Context, sql string, args ...any) map[string]any {
	rows := ds.QueryCtx(ctx, sql, args)
	defer rows.Close()
	for rows.Next() {
		m := map[string]any{}
		err := rows.MapScan(m)
		if err != nil {
			panic(exception.New(err.Error()))
		}
		return m
	}
	if err := rows.Err(); err != nil {
		panic(exception.New(err.Error()))
	}
	return nil
}

func (ds *DataSource) QueryOneString(sql string, args ...any) string {
	return ds.QueryOneStringCtx(context.Background(), sql, args...)
}

func (ds *DataSource) QueryOneStringCtx(ctx context.Context, sql string, args ...any) string {
	rows := ds.QueryCtx(ctx, sql, args)
	defer rows.Close()
	for rows.Next() {
		ret, err := rows.SliceScan()
		if err != nil {
			panic(exception.New(err.Error()))
		}
		return cast.ToString(ret[0])
	}
	if err := rows.Err(); err != nil {
		panic(exception.New(err.Error()))
	}
	return ""
}

func (ds *DataSource) QueryListMap(sql string, args ...any) []map[string]any {
	return ds.QueryListMapCtx(context.Background(), sql, args...)
}

func (ds *DataSource) QueryListMapCtx(ctx context.Context, sql string, args ...any) []map[string]any {
	rows := ds.QueryCtx(ctx, sql, args)
	defer rows.Close()
	list := make([]map[string]any, 0, 5)
	for rows.Next() {
		m := map[string]any{}
		err := rows.MapScan(m)
		if err != nil {
			panic(exception.New(err.Error()))
		}
		list = append(list, m)
	}
	// 迭代错误检查：否则中途出错会静默返回部分数据
	if err := rows.Err(); err != nil {
		panic(exception.New(err.Error()))
	}
	return list
}

func (ds *DataSource) QueryListString(sql string, args ...any) []string {
	return ds.QueryListStringCtx(context.Background(), sql, args...)
}

func (ds *DataSource) QueryListStringCtx(ctx context.Context, sql string, args ...any) []string {
	rows := ds.QueryCtx(ctx, sql, args)
	defer rows.Close()
	list := make([]string, 0, 5)
	for rows.Next() {
		ret, err := rows.SliceScan()
		if err != nil {
			panic(exception.New(err.Error()))
		}
		list = append(list, cast.ToString(ret[0]))
	}
	// 迭代错误检查：否则中途出错会静默返回部分数据
	if err := rows.Err(); err != nil {
		panic(exception.New(err.Error()))
	}
	return list
}

// FormatRawMap 原始查出的map[string]any 转为 map[string]string 用于sql语句中
// 注意防注入
func (ds *DataSource) FormatRawMap(rows map[string]any) map[string]string {
	res := make(map[string]string)
	for key, val := range rows {
		res[key] = ds.FormatRawValue(val)
	}
	return res
}

func (ds *DataSource) FormatRawValue(val any) string {
	if val == nil {
		return "null"
	}
	// todo 其他情况
	switch val.(type) {
	case string:
		// 字面量内的 ' 须双写转义，否则值含引号时破坏 SQL
		return "'" + strings.ReplaceAll(val.(string), "'", "''") + "'"
	case []uint8:
		return "'" + strings.ReplaceAll(string(val.([]uint8)), "'", "''") + "'"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return cast.ToString(val)
	case float32, float64:
		return cast.ToString(val)
	case bool:
		return cast.ToString(val)
	case time.Time:
		t := val.(time.Time)
		if t.IsZero() {
			return "null"
		}
		return "'" + t.In(timekit.GetLocation()).Format(timekit.TimeLayout) + "'"
	}
	return "'" + strings.ReplaceAll(cast.ToString(val), "'", "''") + "'"
}

// QueryColumnDef 获取表的列结构
func (ds *DataSource) QueryColumnDef(table string) []ColumnSchema {
	var maps []map[string]any
	// 参数化查询，避免 table/schema 含特殊字符引发注入
	p1 := rawPlaceholder(ds.Driver, 1)
	p2 := rawPlaceholder(ds.Driver, 2)
	switch ds.Driver {
	case sqlconst.DM, sqlconst.Oracle:
		// todo select * from user_col_comments where TABLE_NAME='某表名称'；
		maps = ds.QueryListMap(
			"SELECT COLUMN_NAME as name, DATA_TYPE as type, NULLABLE as nullable FROM ALL_TAB_COLUMNS WHERE TABLE_NAME = "+p1+" and OWNER="+p2,
			table, ds.Schema)
	default:
		// pg: pg_description
		maps = ds.QueryListMap(
			"SELECT column_name as name, data_type as type, is_nullable as nullable FROM information_schema.columns WHERE table_name = "+p1+" and table_schema="+p2,
			table, ds.Schema)
	}
	res := make([]ColumnSchema, 0, len(maps))
	for _, m := range maps {
		m0 := map[string]any{}
		for k, v := range m {
			m0[strings.ToLower(k)] = v
		}
		res = append(res, ColumnSchema{
			Name:     cast.ToString(m0["name"]),
			Type:     cast.ToString(m0["type"]),
			Nullable: cast.ToBool(m0["nullable"]),
		})
	}
	return res
}
