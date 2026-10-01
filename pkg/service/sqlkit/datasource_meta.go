package sqlkit

import (
	"strings"

	"github.com/example/go-frame/pkg/class/const/sqlconst"
	"github.com/spf13/cast"
)

// QueryTables 列出当前 schema（ds.Schema）下的基础表，不含视图。
// mysql 系（含 doris）走 information_schema；dm/oracle 走 ALL_TABLES；pg 系走 information_schema。
func (ds *DataSource) QueryTables() []string {
	p1 := rawPlaceholder(ds.Driver, 1)
	var maps []map[string]any
	switch ds.Driver {
	case sqlconst.DM, sqlconst.Oracle:
		maps = ds.QueryListMap("SELECT TABLE_NAME FROM ALL_TABLES WHERE OWNER = "+p1, ds.Schema)
	default:
		maps = ds.QueryListMap(
			"SELECT table_name FROM information_schema.tables WHERE table_schema = "+p1+" AND table_type = 'BASE TABLE'",
			ds.Schema)
	}
	res := make([]string, 0, len(maps))
	for _, m := range maps {
		for k, v := range m {
			if strings.EqualFold(k, "table_name") {
				res = append(res, cast.ToString(v))
				break
			}
		}
	}
	return res
}

// QueryPrimaryKeys 按序返回表的主键列；无主键返回空。
func (ds *DataSource) QueryPrimaryKeys(table string) []string {
	p1 := rawPlaceholder(ds.Driver, 1)
	p2 := rawPlaceholder(ds.Driver, 2)
	var maps []map[string]any
	switch ds.Driver {
	case sqlconst.DM, sqlconst.Oracle:
		maps = ds.QueryListMap(
			"SELECT cols.COLUMN_NAME FROM ALL_CONSTRAINTS con JOIN ALL_CONS_COLUMNS cols "+
				"ON con.OWNER = cols.OWNER AND con.CONSTRAINT_NAME = cols.CONSTRAINT_NAME "+
				"WHERE con.CONSTRAINT_TYPE = 'P' AND con.OWNER = "+p1+" AND con.TABLE_NAME = "+p2+
				" ORDER BY cols.POSITION",
			ds.Schema, table)
	case sqlconst.Postgres, sqlconst.Kingbase:
		maps = ds.QueryListMap(
			"SELECT kcu.column_name FROM information_schema.table_constraints tc "+
				"JOIN information_schema.key_column_usage kcu ON tc.constraint_name = kcu.constraint_name "+
				"AND tc.table_schema = kcu.table_schema "+
				"WHERE tc.constraint_type = 'PRIMARY KEY' AND tc.table_schema = "+p1+" AND tc.table_name = "+p2+
				" ORDER BY kcu.ordinal_position",
			ds.Schema, table)
	default:
		maps = ds.QueryListMap(
			"SELECT COLUMN_NAME FROM information_schema.key_column_usage "+
				"WHERE TABLE_SCHEMA = "+p1+" AND TABLE_NAME = "+p2+" AND CONSTRAINT_NAME = 'PRIMARY' "+
				"ORDER BY ORDINAL_POSITION",
			ds.Schema, table)
	}
	res := make([]string, 0, len(maps))
	for _, m := range maps {
		for k, v := range m {
			if strings.EqualFold(k, "column_name") {
				res = append(res, cast.ToString(v))
				break
			}
		}
	}
	return res
}
