package sqlkit

import (
	"github.com/Masterminds/squirrel"
	"github.com/example/go-frame/pkg/class/const/sqlconst"
	"github.com/example/go-frame/pkg/class/exception"
)

// 默认取modelmeta中的columns，并装饰引号；fields中不装饰，因为可能存在表达式
func (dao Dao[T]) _select(fields ...string) SelectDao[T] {
	ldv := LogicDelVal
	if len(dao.LogicDelVal) > 0 {
		ldv = dao.LogicDelVal
	}
	d := SelectDao[T]{
		builder: squirrel.Select(fields...),
		Dao:     dao,
	}
	d.LogicDelVal = ldv
	return d
}

func (dao Dao[T]) Select(fields ...string) SelectDao[T] {
	if len(fields) == 0 {
		return dao.SelectEx()
	}
	// S13: 校验空串，避免生成非法 SQL
	for _, f := range fields {
		if f == "" {
			panic(exception.New("select fields 不能包含空字符串"))
		}
	}
	return dao._select(fields...)
}

// SelectEx 在modelmeta columns中去掉指定的字段
func (dao Dao[T]) SelectEx(fields ...string) SelectDao[T] {
	return dao.SelectPrefix("", fields...)
}

// SelectPrefix 在modelmeta的字段前增加prefix
func (dao Dao[T]) SelectPrefix(prefix string, without ...string) SelectDao[T] {
	if dao.modelMeta.tableName == "" {
		panic(exception.New("sqlbuilder modelmeta null"))
	}
	return dao._select(dao.modelMeta.getSelectColumnsWithPrefix(prefix, without...)...)
}

func (dao Dao[T]) Update() UpdateDao[T] {
	if dao.modelMeta.tableName == "" {
		panic(exception.New("sqlbuilder modelmeta null"))
	}
	ldv := LogicDelVal
	if len(dao.LogicDelVal) > 0 {
		ldv = dao.LogicDelVal
	}
	d := UpdateDao[T]{
		builder: squirrel.Update(dao.modelMeta.getTable(dao.dataSource)),
		Dao:     dao,
	}
	d.LogicDelVal = ldv
	return d
}

func (dao Dao[T]) Delete() DeleteDao[T] {
	if dao.modelMeta.tableName == "" {
		panic(exception.New("sqlbuilder modelmeta null"))
	}
	d := DeleteDao[T]{
		builder: squirrel.Delete(dao.modelMeta.getTable(dao.dataSource)),
		Dao:     dao,
	}
	ldv := LogicDelVal
	if len(dao.LogicDelVal) > 0 {
		ldv = dao.LogicDelVal
	}
	d.LogicDelVal = ldv
	return d
}

func (dao Dao[T]) Insert() InsertDao[T] {
	if dao.modelMeta.tableName == "" {
		panic(exception.New("sqlbuilder modelmeta null"))
	}
	d := InsertDao[T]{
		builder: squirrel.Insert(dao.modelMeta.getTable(dao.dataSource)),
		Dao:     dao,
	}
	ldv := LogicDelVal
	if len(dao.LogicDelVal) > 0 {
		ldv = dao.LogicDelVal
	}
	d.LogicDelVal = ldv
	return d
}

func (dao Dao[T]) Replace() InsertDao[T] {
	if dao.modelMeta.tableName == "" {
		panic(exception.New("sqlbuilder modelmeta null"))
	}
	// REPLACE INTO 语法仅 MySQL/SQLite 支持，其他驱动提前报错而非生成非法 SQL
	if dao.dataSource.Driver != sqlconst.Mysql && dao.dataSource.Driver != sqlconst.Sqlite3 {
		panic(exception.New("Replace 仅支持 MySQL/SQLite，其他驱动请用 UpsertObj/InsertObjIgnoreConflict"))
	}
	d := InsertDao[T]{
		builder: squirrel.Replace(dao.modelMeta.getTable(dao.dataSource)),
		Dao:     dao,
	}
	ldv := LogicDelVal
	if len(dao.LogicDelVal) > 0 {
		ldv = dao.LogicDelVal
	}
	d.LogicDelVal = ldv
	return d
}
