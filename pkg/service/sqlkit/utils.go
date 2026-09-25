package sqlkit

import (
	"context"
	"reflect"
	"regexp"

	"github.com/example/go-frame/pkg/class"
	"github.com/example/go-frame/pkg/class/constraints"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/jmoiron/sqlx"
)

// schemaNamePattern 合法 schema 名：字母/下划线开头，仅含字母数字下划线。
var schemaNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ValidateSchemaName 校验 schema 名格式，防止通过 schema 名注入。
func ValidateSchemaName(schema string) {
	if schema == "" {
		panic(exception.New("schema不能为空"))
	}
	if !schemaNamePattern.MatchString(schema) {
		panic(exception.New("schema名格式非法"))
	}
}

func scanObjList[T any](dao SelectDao[T]) []*T {
	return scanObjListCtx(context.Background(), dao)
}

func scanObjListCtx[T any](ctx context.Context, dao SelectDao[T]) []*T {
	rows := dao.QueryRowsCtx(ctx)
	list := make([]*T, 0, 5)
	defer rows.Close()
	for rows.Next() {
		list = append(list, scanStruct[T](rows, dao.dataSource.Driver, dao.modelMeta))
	}
	// S6: 检查迭代错误，区分"无数据"与"查询失败"
	if err := rows.Err(); err != nil {
		panic(exception.New(err.Error()))
	}
	// S12: 优先批量级联，避免 N+1
	cascadeList(dao.Dao, list)
	return list
}

// scanStruct StructScan 后按 ModelMeta.scanFields 做后处理（SetDBDriver / decimal 精度）。
// S18: 只遍历 init 时预计算的后处理字段，避免每行全字段 reflect 与 tag 解析。
func scanStruct[T any](rows *sqlx.Rows, driver string, meta ModelMeta) *T {
	m := new(T)
	err := rows.StructScan(m)
	// err 前置：扫描失败时不再对零值结构做后处理
	if err != nil {
		panic(exception.New(err.Error()))
	}
	if len(meta.scanFields) > 0 {
		rv := reflect.ValueOf(m).Elem()
		for i := range meta.scanFields {
			sf := &meta.scanFields[i]
			v := rv.Field(sf.structIndex)
			obj := v.Addr().Interface()
			// 处理 arr, 只针对 struct; 设置 dbdriver
			if sf.setDriver {
				if vv, ok := obj.(constraints.SetDBDriverInterface); ok {
					vv.SetDBDriver(driver)
				}
			}
			// 对decimal精度的处理
			if sf.decimalPrecision > 0 {
				if vv, ok := obj.(class.Decimal); ok {
					vv.Set(vv.Round(sf.decimalPrecision))
				}
				if vv, ok := obj.(*class.Decimal); ok {
					vv.Set(vv.Round(sf.decimalPrecision))
				}
			}
		}
	}
	return m
}

// insertBatchChunkSize 单条 INSERT 语句的最大行数。PG 协议单语句参数上限 65535，
// 超限直接报错；MySQL 受 max_allowed_packet 约束。按列数推导行数上限并留一倍余量。
func insertBatchChunkSize(cols int) int {
	const maxParamsPerStmt = 32768
	if cols <= 0 {
		return 1
	}
	n := maxParamsPerStmt / cols
	if n < 1 {
		n = 1
	}
	return n
}
