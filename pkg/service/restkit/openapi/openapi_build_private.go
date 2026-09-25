package openapi

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/cli/tag"
	"github.com/example/go-frame/pkg/library/arraykit"
	"github.com/example/go-frame/pkg/library/stringkit"
)

var refPrefix = "#/components/schemas/"

// buildSchemaByType 将 Go 类型映射为 OpenAPI schema。
// 用 reflect.Kind + 类型名精确匹配，替代旧的 strings.Contains 模糊匹配。
//
// 说明：
//   - class.Int64 等 NullableInt64 类型 → integer/int64。JSON 序列化虽然输出为字符串以防 JS 溢出，
//     但 schema 用 integer/int64 保持规范正确，前端用 BigInt 解析即可。
//   - class.Float64 → number/double（同理保持规范正确）。
//   - class.Decimal → number/double（不是 string，保持规范正确）。
//   - class.Time → string/date-time。
//   - map[string]X / class.MapString → object + additionalProperties。
//   - 切片/数组 → array。
func buildSchemaByType(t reflect.Type) *ApiDocV3Schema {
	// 先解引用指针
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	schema := &ApiDocV3Schema{}

	// 按"类型名字"精确处理 class.* 自定义类型
	typeName := t.String()
	switch typeName {
	case "class.File":
		schema.Type = SchemaTypeString
		schema.Format = SchemaFormatBinary
		return schema
	case "class.String":
		schema.Type = SchemaTypeString
		return schema
	case "class.Int32":
		schema.Type = SchemaTypeInteger
		schema.Format = SchemaFormatInt32
		return schema
	case "class.Int64":
		schema.Type = SchemaTypeInteger
		schema.Format = SchemaFormatInt64
		return schema
	case "class.Float64":
		schema.Type = SchemaTypeNumber
		schema.Format = SchemaFormatDouble
		return schema
	case "class.Decimal":
		schema.Type = SchemaTypeNumber
		schema.Format = SchemaFormatDouble
		return schema
	case "class.Bool":
		schema.Type = SchemaTypeBool
		return schema
	case "class.Time":
		schema.Type = SchemaTypeString
		schema.Format = SchemaFormatDateTime
		return schema
	case "class.ArrString":
		schema.Type = SchemaTypeArray
		schema.Items = &ApiDocV3Schema{Type: SchemaTypeString}
		return schema
	case "class.ArrInt":
		schema.Type = SchemaTypeArray
		schema.Items = &ApiDocV3Schema{Type: SchemaTypeInteger, Format: SchemaFormatInt64}
		return schema
	case "class.MapString", "class.MapStringSync", "*class.MapStringSync":
		schema.Type = SchemaTypeObject
		schema.AdditionalProperties = &ApiDocV3Schema{} // 空schema表示任意类型
		return schema
	case "class.MapStringArr":
		schema.Type = SchemaTypeArray
		schema.Items = &ApiDocV3Schema{
			Type:                 SchemaTypeObject,
			AdditionalProperties: &ApiDocV3Schema{},
		}
		return schema
	}

	// 按 Kind 处理基本类型
	switch t.Kind() {
	case reflect.String:
		schema.Type = SchemaTypeString
	case reflect.Bool:
		schema.Type = SchemaTypeBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32:
		schema.Type = SchemaTypeInteger
		schema.Format = SchemaFormatInt32
	case reflect.Int64:
		schema.Type = SchemaTypeInteger
		schema.Format = SchemaFormatInt64
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		schema.Type = SchemaTypeInteger
		schema.Format = SchemaFormatInt32
	case reflect.Uint64:
		schema.Type = SchemaTypeInteger
		schema.Format = SchemaFormatInt64
	case reflect.Float32:
		schema.Type = SchemaTypeNumber
		schema.Format = SchemaFormatFloat
	case reflect.Float64:
		schema.Type = SchemaTypeNumber
		schema.Format = SchemaFormatDouble
	case reflect.Map:
		schema.Type = SchemaTypeObject
		// map[string]X → additionalProperties = schema(X)
		if t.Key().Kind() == reflect.String {
			elemSchema := buildSchemaByType(t.Elem())
			if elemSchema != nil {
				schema.AdditionalProperties = elemSchema
			} else {
				schema.AdditionalProperties = &ApiDocV3Schema{}
			}
		} else {
			schema.AdditionalProperties = &ApiDocV3Schema{}
		}
	case reflect.Slice, reflect.Array:
		schema.Type = SchemaTypeArray
		itemSchema := buildSchemaByType(t.Elem())
		if itemSchema == nil {
			itemSchema = &ApiDocV3Schema{Type: SchemaTypeString}
		}
		schema.Items = itemSchema
	case reflect.Struct:
		// $ref 与 type 共存时，3.0 会忽略 type；3.1 允许共存但不规范。
		// 这里只设 Ref，不设 Type，保持规范正确。
		schema.Ref = buildComponentSchema(t)
	case reflect.Interface:
		// 空接口表示任意类型
		// 不设置 Type，留空表示 any
	default:
		schema.Type = SchemaTypeString
	}
	return schema
}

// fieldNameAndSkip 从 struct field 中提取 OpenAPI 字段名。
// 优先级：json tag（处理 json:"-" 跳过；json:"name,omitempty" 取 name）→ 无 json tag 时用 LowerFirst(field.Name)。
// 返回 (name, skip)。skip=true 表示该字段不应出现在 schema 中。
//
// 命名规则必须与 context.fieldKey 同源（stringkit.LowerFirst）：
// 原先本包私有一套 lowerFirst（ID→Id）与绑定侧（ID→iD）不一致，
// 文档展示的参数名会和实际可绑定的参数名对不上。
func fieldNameAndSkip(field reflect.StructField) (string, bool) {
	jsonTag := field.Tag.Get("json")
	if jsonTag != "" {
		// json:"-"：完全跳过
		if jsonTag == "-" {
			return "", true
		}
		// json:"name,omitempty"：取逗号前的 name
		name := jsonTag
		if idx := strings.Index(jsonTag, ","); idx >= 0 {
			name = jsonTag[:idx]
		}
		if name == "" {
			// json:",omitempty" 无名 → 用字段名 LowerFirst
			return stringkit.LowerFirst(field.Name), false
		}
		return name, false
	}
	// 无 json tag：用 LowerFirst(field.Name)
	return stringkit.LowerFirst(field.Name), false
}

// buildFieldSchemas 统一封装对象的成员变量为 schema，并回调处理。
// 处理 json:"-" 跳过、schema:"ignore" 跳过、validate 约束提取。
// A14: 将 fieldNameAndSkip 计算得到的 name 直接传给 callback，
// 避免 callback 内重复调用 fieldNameAndSkip（原逻辑每个字段会被解析两次）。
func buildFieldSchemas(rt reflect.Type, callBack func(s *ApiDocV3Schema, field reflect.StructField, name string)) {
	for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Array {
		rt = rt.Elem()
	}
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		// schema:"ignore" 跳过
		if tag.Schema.Contain(field.Tag, tag.SchemaIgnore) {
			continue
		}
		// json:"-" 跳过
		name, skip := fieldNameAndSkip(field)
		if skip {
			continue
		}
		schema := buildSchemaByType(field.Type)
		schema.Description = field.Tag.Get(tag.Comment.Name)
		if v, ok := field.Tag.Lookup(tag.Default.Name); ok {
			schema.Default = v
		}
		applyValidateConstraints(schema, field.Tag.Get(tag.Validate.Name))
		if v, ok := field.Tag.Lookup(tag.Example.Name); ok {
			schema.Example = v
		}
		if tag.Deprecated.Hit(field.Tag) {
			schema.Deprecated = true
		}
		if tag.ReadOnly.Hit(field.Tag) {
			schema.ReadOnly = true
		}
		if tag.WriteOnly.Hit(field.Tag) {
			schema.WriteOnly = true
		}
		callBack(schema, field, name)
	}
}

// applyValidateConstraints 从 validate tag 提取约束写入 schema。
// 支持：required、lt=N（maximum）、lte=N（maximum）、gt=N（minimum）、gte=N（minimum）、
// min=N（minLength）、max=N（maxLength）、oneof=a b c（enum）、len=N（minLength+maxLength）。
func applyValidateConstraints(schema *ApiDocV3Schema, validateTag string) {
	if validateTag == "" {
		return
	}
	parts := strings.Split(validateTag, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key := part
		val := ""
		if idx := strings.Index(part, "="); idx >= 0 {
			key = part[:idx]
			val = part[idx+1:]
		}
		switch key {
		case tag.ValidateRequired:
			// required 在 buildObjectSchema 中处理
		case "lt", "lte":
			// lt 为 exclusive，但 3.1 的 exclusiveMaximum 需额外字段；
			// 当前简化为 lte（边界可达），如需 exclusive 后续在 schema 上补 exclusiveMaximum 字段
			if n, err := strconv.ParseFloat(val, 64); err == nil {
				schema.Maximum = &n
			}
		case "gt", "gte":
			if n, err := strconv.ParseFloat(val, 64); err == nil {
				schema.Minimum = &n
			}
		case "min":
			// 字符串最小长度
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				schema.MinLength = &n
			}
		case "max":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				schema.MaxLength = &n
			}
		case "len":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				schema.MinLength = &n
				schema.MaxLength = &n
			}
		case "oneof":
			// oneof=a b c → enum=[a,b,c]
			if val != "" {
				items := strings.Fields(val)
				enum := make([]any, 0, len(items))
				for _, item := range items {
					enum = append(enum, item)
				}
				schema.Enum = enum
			}
		}
	}
}

// buildObjectSchema 将 struct 类型构建为 type=object 的 schema。
func buildObjectSchema(rt reflect.Type) *ApiDocV3Schema {
	schema := &ApiDocV3Schema{Properties: map[string]*ApiDocV3Schema{}}
	schema.Type = SchemaTypeObject
	// A14: name 已由 buildFieldSchemas 计算并传入，无需再次调用 fieldNameAndSkip
	buildFieldSchemas(rt, func(s *ApiDocV3Schema, field reflect.StructField, name string) {
		if tag.Validate.Contain(field.Tag, tag.ValidateRequired) {
			schema.Required = append(schema.Required, name)
		}
		schema.Properties[name] = s
	})
	return schema
}

// buildComponentSchema 将对象写入 components/schemas 并返回 $ref。
// 处理循环引用：先注册空 schema 占位，再立即构建并覆盖。
// key 用包限定名（rt.String()，如 "mod.user.ResLogin"）：裸类型名在跨包同名 struct
// 时会静默互相覆盖，文档只剩最后一个。
func buildComponentSchema(rt reflect.Type) string {
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt.Name() == "" {
		panic(exception.New("openapi components schema name is nil"))
	}
	name := rt.String()
	// 已注册：直接返回 ref（含已注册但 todo 未处理的占位）
	if _, ok := Doc.Components.Schemas[name]; ok {
		return refPrefix + name
	}
	// 占位，防止递归调用重复注册
	Doc.Components.Schemas[name] = &ApiDocV3Schema{}
	// 立即构建（而非延迟），让被引用方在引用方之前构建完成
	// 这样 A→B、B→A 时：处理 A，发现 B 未注册，递归构建 B；
	// B 发现 A 已注册占位，直接返回 ref；B 构建完成写回；
	// A 继续，正确引用 B 的细节。
	schema := buildObjectSchema(rt)
	// 合并 _exclusiveMax/_exclusiveMin 到规范字段
	finalizeSchema(schema)
	Doc.Components.Schemas[name] = schema
	return refPrefix + name
}

// finalizeSchema 递归清理 schema 树（当前为预留钩子，便于后续处理 exclusiveMaximum 等需要后处理的字段）。
func finalizeSchema(s *ApiDocV3Schema) {
	if s == nil {
		return
	}
	for _, p := range s.Properties {
		finalizeSchema(p)
	}
	if s.Items != nil {
		finalizeSchema(s.Items)
	}
	if s.AdditionalProperties != nil {
		finalizeSchema(s.AdditionalProperties)
	}
}

// buildReqParamElement 构建单个参数元素。
// A14: name 由调用方传入，避免每次重复解析 json tag。
func buildReqParamElement(s *ApiDocV3Schema, field reflect.StructField, name string) *ApiDocV3ReqParam {
	e := &ApiDocV3ReqParam{}
	e.Schema = s
	e.Description = field.Tag.Get(tag.Comment.Name)
	if tag.Validate.Contain(field.Tag, tag.ValidateRequired) {
		e.Required = true
	}
	e.Name = name
	in := field.Tag.Get(tag.ParamIn.Name)
	if !arraykit.StringContains([]string{ParamInQuery, ParamInPath, ParamInHeader, ParamInCookie}, in) {
		in = ParamInQuery
	}
	e.In = in
	if in == ParamInPath {
		e.Required = true
	}
	// allowEmptyValue 仅 query 有效
	if tag.AllowEmptyValue.Hit(field.Tag) && in == ParamInQuery {
		e.AllowEmptyValue = true
	}
	// example
	if v, ok := field.Tag.Lookup(tag.Example.Name); ok {
		e.Example = v
	}
	// deprecated
	if tag.Deprecated.Hit(field.Tag) {
		e.Deprecated = true
	}
	return e
}
