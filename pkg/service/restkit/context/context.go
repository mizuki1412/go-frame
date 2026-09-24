package context

import (
	"net/http"
	"reflect"
	"strings"

	"github.com/example/go-frame/pkg/class"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/cli/tag"
	"github.com/example/go-frame/pkg/library/jsonkit"
	"github.com/example/go-frame/pkg/library/stringkit"
	"github.com/example/go-frame/pkg/library/timekit"
	"github.com/example/go-frame/pkg/service/logkit"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/spf13/cast"
)

type Context struct {
	Proxy    *gin.Context
	Request  *http.Request
	Response gin.ResponseWriter
}

// Set msg per request
func (ctx *Context) Set(key string, val any) {
	ctx.Proxy.Set(key, val)
}

func (ctx *Context) Get(key string) any {
	r, _ := ctx.Proxy.Get(key)
	return r
}

// data: query, form, json/xml, param

// BindForm bean 指针、bean 必须是 struct 定义过的
func (ctx *Context) BindForm(bean any) {
	ctx.bindStruct(bean)
	err := Validator.Struct(bean)
	if err != nil {
		if _, ok := err.(*validator.InvalidValidationError); ok {
			panic(exception.New(err.Error()))
		}
		for _, err0 := range err.(validator.ValidationErrors) {
			panic(exception.New("validation failed: " + stringkit.LowerFirst(err0.Field()) + ", need " + err0.Tag()))
		}
	}
	body := jsonkit.ToString(bean)
	if len(body) > 1024 {
		body = body[:1024]
	}
	// P2 修复：请求体含 phone/pwd 等 PII，Info 级会落进常规日志；
	// 降为 debug（默认日志级别 info 下不打，排查时显式调 log.level=debug）
	logkit.Debug("request-body", "token", ctx.GetToken(), "body", body)
}

// fieldKey 从 struct field 提取请求参数 key。
// B6: 优先使用 json tag（json:"-" 跳过；json:"name,omitempty" 取 name），
// 无 json tag 时回退到 LowerFirst(field.Name)。
func fieldKey(field reflect.StructField) (string, bool) {
	if jsonTag := field.Tag.Get("json"); jsonTag != "" {
		if jsonTag == "-" {
			return "", true
		}
		name := jsonTag
		if before, _, ok := strings.Cut(jsonTag, ","); ok {
			name = before
		}
		if name == "" {
			return stringkit.LowerFirst(field.Name), false
		}
		return name, false
	}
	return stringkit.LowerFirst(field.Name), false
}

// bindValue 从 PostForm/Query/Param 三处合并获取值。
// B5: keyExist 跟踪所有来源是否存在 key（任一来源命中即为 true），
// 不再仅依赖 GetPostForm，避免 Query/Param 覆盖时 class.String 无法绑定。
func (ctx *Context) bindValue(key string) (val string, keyExist bool) {
	if v, ok := ctx.Proxy.GetPostForm(key); ok {
		return v, true
	}
	if v, ok := ctx.Proxy.GetQuery(key); ok {
		return v, true
	}
	// Param：gin.Context.Params 暴露为切片，遍历判断存在性
	for _, p := range ctx.Proxy.Params {
		if p.Key == key {
			return p.Value, true
		}
	}
	return "", false
}

// binderFunc 将字符串值绑定到 reflect.Value。
// 参数：
//   - fieldV: 目标字段值
//   - val: 已合并并 trim 后的字符串
//   - keyExist: 请求中是否提供该 key（用于区分"未传"与"传了空值"，class.String 依赖此标志）
//   - field: 字段元信息（用于读取 tag.DecimalPrecision 等约束）
type binderFunc func(fieldV reflect.Value, val string, keyExist bool, field reflect.StructField)

// binders A5: 用类型名 → binder 函数的 map 替代冗长的 type switch，
// 新增类型只需追加一行注册，bindStruct 主体保持简洁。
var binders = map[string]binderFunc{
	"string": func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		fieldV.SetString(val)
	},
	"int32": setIntKind,
	"int":   setIntKind,
	"int64": setIntKind,
	"int8":  setIntKind,
	"int16": setIntKind,
	"byte":  setIntKind,
	"float64": func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if !stringkit.IsNull(val) {
			fieldV.SetFloat(cast.ToFloat64(val))
		}
	},
	"bool": func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if !stringkit.IsNull(val) {
			fieldV.SetBool(cast.ToBool(val))
		}
	},
	"class.Int32": func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if !stringkit.IsNull(val) {
			fieldV.Set(reflect.ValueOf(class.NewInt32(val)))
		}
	},
	"class.Int64": func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if !stringkit.IsNull(val) {
			fieldV.Set(reflect.ValueOf(class.NewInt64(val)))
		}
	},
	"class.Float64": func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if !stringkit.IsNull(val) {
			fieldV.Set(reflect.ValueOf(class.NewFloat64(val)))
		}
	},
	"class.Bool": func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if !stringkit.IsNull(val) {
			fieldV.Set(reflect.ValueOf(class.NewBool(val)))
		}
	},
	"class.String": func(fieldV reflect.Value, val string, keyExist bool, _ reflect.StructField) {
		// 仅当请求中存在该 key 时才赋值，区分"未传"与"传空串"
		if keyExist {
			fieldV.Set(reflect.ValueOf(class.NewString(val)))
		}
	},
	"class.ArrInt": func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if stringkit.IsNull(val) {
			return
		}
		var p []int64
		_ = jsonkit.ParseObj(val, &p)
		fieldV.Set(reflect.ValueOf(class.NewArrInt(p)))
	},
	"class.ArrString": func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if stringkit.IsNull(val) {
			return
		}
		var p []string
		_ = jsonkit.ParseObj(val, &p)
		fieldV.Set(reflect.ValueOf(class.NewArrString(p)))
	},
	"class.MapString": func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if stringkit.IsNull(val) {
			return
		}
		var p map[string]any
		_ = jsonkit.ParseObj(val, &p)
		fieldV.Set(reflect.ValueOf(class.NewMapString(p)))
	},
	"class.MapStringArr": func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if stringkit.IsNull(val) {
			return
		}
		var p []map[string]any
		_ = jsonkit.ParseObj(val, &p)
		fieldV.Set(reflect.ValueOf(class.NewMapStringArr(p)))
	},
	"class.Time": func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if stringkit.IsNull(val) {
			return
		}
		temp := class.Time{}
		if s, err := timekit.Parse(val); err == nil {
			temp.Set(s)
		}
		fieldV.Set(reflect.ValueOf(temp))
	},
	"class.Decimal": func(fieldV reflect.Value, val string, _ bool, field reflect.StructField) {
		if stringkit.IsNull(val) {
			return
		}
		tmp := class.Decimal{}
		tmp.Set(val)
		if precision := cast.ToInt32(field.Tag.Get(tag.DecimalPrecision.Name)); precision > 0 {
			tmp.Set(tmp.Round(precision))
		}
		fieldV.Set(reflect.ValueOf(tmp))
	},
}

// setIntKind 共用整数类型绑定，避免 map 中重复定义。
func setIntKind(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
	if !stringkit.IsNull(val) {
		fieldV.SetInt(cast.ToInt64(val))
	}
}

// 实现form/query/json中的数据合并获取。
// comment:"xxx" default:"" trim:"true"
func (ctx *Context) bindStruct(bean any) {
	rt0 := reflect.TypeOf(bean)
	if rt0.Kind() != reflect.Pointer {
		panic(exception.New("bindStruct need pointer"))
	}
	rt := rt0.Elem()
	rv := reflect.ValueOf(bean).Elem()
	// 取json和取form只能同时进行一次，取完，流被关闭了。
	isJson := strings.Index(ctx.Request.Header.Get("content-type"), "application/json") >= 0
	if isJson {
		// P1 修复：原实现 `_ =` 吞掉绑定错误——非法 JSON 会静默变成零值 bean，
		// 与 form 路径（明确报错）行为不一致，必填项靠 validator 兜底、非必填项静默丢失
		if err := ctx.Proxy.ShouldBindJSON(bean); err != nil {
			panic(exception.New("请求数据解析失败: " + err.Error()))
		}
		return
	}
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		fieldV := rv.Field(i)
		typeString := field.Type.String()
		// B6: json tag 决定参数 key（json:"-" 表示跳过该字段不绑定）
		key, skip := fieldKey(field)
		if skip {
			continue
		}
		// multipart file
		if typeString == "class.File" {
			file, err := ctx.Proxy.FormFile(key)
			// 如果文件流必须存在则检测
			if err != nil && tag.Validate.Contain(field.Tag, tag.ValidateRequired) {
				panic(exception.New(err.Error()))
			}
			if err == nil {
				f, e := file.Open()
				if e == nil {
					fieldV.Set(reflect.ValueOf(class.File{
						File:   f,
						Header: file,
					}))
				} else {
					logkit.Error(e.Error())
				}
			}
			continue
		}
		// B5: 跨 PostForm/Query/Param 统一判断 key 是否存在
		val, keyExist := ctx.bindValue(key)
		if tag.Trim.Hit(field.Tag) {
			val = strings.TrimSpace(val)
		}
		if val == "" {
			if tag.Default.Exist(field.Tag) {
				val = field.Tag.Get(tag.Default.Name)
				keyExist = true
			}
		}
		// A5: 查表绑定，未注册类型保持零值
		if binder, ok := binders[typeString]; ok {
			binder(fieldV, val, keyExist, field)
		}
	}
}
