package sqlkit

import (
	"reflect"

	"github.com/example/go-frame/pkg/class"
	"github.com/example/go-frame/pkg/class/constraints"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/cli/tag"
)

// ModelMeta 获取model中的tablename和db fields
// S20: 移除 dateSource 字段——缓存实例不再持有数据源状态，消除多 goroutine 不同 schema
// 覆盖 dateSource 的并发竞争。所有需要 dataSource 的方法改为接收 ds 参数。
type ModelMeta struct {
	tableName   string
	keys        []ModelMetaKey
	logicDelKey ModelMetaKey
	driver      string // 仅用于缓存 key 生成，不作为状态
	// 处理后的 keys array
	// 用于 select 的 全量columns（已 escape，按 driver 生成）
	allSelectColumns []string
	// 与 allSelectColumns 一一对应的未 escape 列名，供 SelectEx/SelectPrefix 排除比对
	allSelectOriKeys []string
	allInsertKeys    []ModelMetaKey
	allUpdateKeys    []ModelMetaKey
	allPKs           []ModelMetaKey
	// S18: 预计算需要后处理的字段（SetDBDriver / decimal precision），避免 scan 时全字段 reflect
	scanFields []scanFieldMeta
}

// scanFieldMeta 描述 scan 后需要后处理的字段索引与动作
type scanFieldMeta struct {
	fieldName   string
	structIndex int
	// 是否需要 SetDBDriver
	setDriver bool
	// decimal precision，>0 表示需要 Round
	decimalPrecision int32
}

// ModelMetaKey 除 logicdelete 外的 keys
type ModelMetaKey struct {
	// escape 后的 key
	Key string
	// 没有 escape 的 key
	OriKey  string
	RStruct reflect.StructField
	Primary bool
	Auto    bool
}

func (th ModelMetaKey) val(rv reflect.Value, driver string) any {
	var val any
	// FieldByIndex 直取，避免 FieldByName 的线性查找（RStruct 即出自同一类型）
	v := rv.FieldByIndex(th.RStruct.Index)
	if v.IsValid() {
		val = v.Interface()
	}
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return nil
	}
	// 改用 isValid() 判断结构体, 为了一致，必须值接收器
	if val != nil && (v.Kind() == reflect.Struct || v.Kind() == reflect.Pointer) {
		if valm, ok := val.(constraints.IsValidInterface); ok {
			if !valm.IsValid() {
				return nil
			}
			if v.Kind() == reflect.Struct {
				if vv, ok := v.Addr().Interface().(constraints.SetDBDriverInterface); ok {
					vv.SetDBDriver(driver)
					val = vv
				}
			} else {
				if vv, ok := v.Interface().(constraints.SetDBDriverInterface); ok {
					vv.SetDBDriver(driver)
					val = vv
				}
			}
		}
	}
	return val
}

// 用于存放model的解析数据： key：包路径+类名+驱动类型
var modelMetaCache = class.NMapStringSync()

// InitModelMeta obj should be elem
// S20: dateSource 由参数传入，不再存入 ModelMeta 字段。
func (th ModelMeta) init(obj any, ds *DataSource) ModelMeta {
	if ds == nil {
		panic(exception.New("dataSource is nil"))
	}
	if obj == nil {
		return ModelMeta{}
	}
	rt := reflect.TypeOf(obj)
	// 包路径+类名+驱动类型
	tk := rt.PkgPath() + "/" + rt.Name() + ":" + ds.Driver
	if cached, ok := modelMetaCache.Get(tk).(ModelMeta); ok {
		// 命中缓存：ModelMeta 已无状态，直接返回
		return cached
	}
	if rt.Kind() != reflect.Struct {
		panic(exception.New("dao model must struct"))
	}
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Tag.Get(tag.DBField.Name)
		if name == "" {
			continue
		}
		oriKey := name
		name = ds.EscapeName(name)
		key := ModelMetaKey{Key: name, OriKey: oriKey, RStruct: rt.Field(i)}
		// tableName; fetch once
		if th.tableName == "" {
			if t, ok := rt.Field(i).Tag.Lookup(tag.DBTable.Name); ok {
				th.tableName = t
			} else if t, ok := rt.Field(i).Tag.Lookup("tablename"); ok {
				// Deprecated:
				th.tableName = t
			}
		}
		if tag.DBColumnLogicDel.Hit(rt.Field(i).Tag) {
			th.logicDelKey = key
			continue
		}
		// pk
		if tag.DBPk.Hit(rt.Field(i).Tag) {
			key.Primary = true
		}
		if tag.DBPkAuto.Hit(rt.Field(i).Tag) {
			key.Auto = true
		}
		th.keys = append(th.keys, key)
	}
	if th.tableName == "" {
		panic(exception.New("model meta tableName error"))
	}
	// 处理
	for _, e := range th.keys {
		th.allSelectColumns = append(th.allSelectColumns, e.Key)
		th.allSelectOriKeys = append(th.allSelectOriKeys, e.OriKey)
		if e.Primary {
			th.allPKs = append(th.allPKs, e)
		}
		if !e.Primary && !e.Auto {
			th.allUpdateKeys = append(th.allUpdateKeys, e)
		}
		if !e.Auto {
			th.allInsertKeys = append(th.allInsertKeys, e)
		}
	}
	if th.logicDelKey.OriKey != "" {
		th.allSelectColumns = append(th.allSelectColumns, th.logicDelKey.Key)
		th.allSelectOriKeys = append(th.allSelectOriKeys, th.logicDelKey.OriKey)
		th.allUpdateKeys = append(th.allUpdateKeys, th.logicDelKey)
	}
	th.driver = ds.Driver
	// S18: 预计算 scan 后需后处理的字段
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Type.Kind() != reflect.Struct {
			continue
		}
		sf := scanFieldMeta{fieldName: f.Name, structIndex: i}
		if f.Type.Implements(reflect.TypeOf((*constraints.SetDBDriverInterface)(nil)).Elem()) ||
			reflect.PointerTo(f.Type).Implements(reflect.TypeOf((*constraints.SetDBDriverInterface)(nil)).Elem()) {
			sf.setDriver = true
		}
		sf.decimalPrecision = toInt32Tag(f.Tag.Get(tag.DecimalPrecision.Name))
		if sf.setDriver || sf.decimalPrecision > 0 {
			th.scanFields = append(th.scanFields, sf)
		}
	}
	modelMetaCache.PutIfAbsent(tk, th)
	return th
}

func toInt32Tag(s string) int32 {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return int32(n)
}

func (th ModelMeta) getSelectColumns(excludes ...string) []string {
	return th.getSelectColumnsWithPrefix("", excludes...)
}

func (th ModelMeta) getSelectColumnsWithPrefix(prefix string, excludes ...string) []string {
	if prefix != "" {
		prefix += "."
	}
	arr := make([]string, 0, len(th.allSelectColumns))
	if len(excludes) > 0 {
		// excludes 是调用方传入的未 escape 字段名，须与 OriKey 比对；
		// allSelectColumns 是 escape 后的列名，直接比对永远不命中
		ex := make(map[string]struct{}, len(excludes))
		for _, e := range excludes {
			ex[e] = struct{}{}
		}
		for i, e := range th.allSelectColumns {
			if _, ok := ex[th.allSelectOriKeys[i]]; !ok {
				arr = append(arr, prefix+e)
			}
		}
	} else if prefix != "" {
		for _, e := range th.allSelectColumns {
			arr = append(arr, prefix+e)
		}
	} else {
		for _, e := range th.allSelectColumns {
			arr = append(arr, e)
		}
	}
	if len(arr) == 0 {
		panic(exception.New("sql columns 不能为空"))
	}
	return arr
}

// getTable alias 可以包括table别名
// S20: 接收 ds 参数，不再依赖 ModelMeta 内部状态。
func (th ModelMeta) getTable(ds *DataSource, alias ...string) string {
	if len(alias) > 0 {
		return ds.DecoTableName(th.tableName) + " AS " + alias[0]
	}
	return ds.DecoTableName(th.tableName)
}

// S20: escapeNames / escapeName 直接委托 DataSource，不再持有 dateSource。
func (th ModelMeta) escapeNames(ds *DataSource, name []string) []string {
	if len(name) == 0 {
		panic(exception.New("modelmeta escapename nil"))
	}
	ret := make([]string, len(name))
	for i := range name {
		ret[i] = ds.EscapeName(name[i])
	}
	return ret
}

func (th ModelMeta) escapeName(ds *DataSource, name string) string {
	return ds.EscapeName(name)
}
