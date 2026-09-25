package context

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"strings"

	"github.com/example/go-frame/pkg/class"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/cli/tag"
	"github.com/example/go-frame/pkg/library/jsonkit"
	"github.com/example/go-frame/pkg/library/stringkit"
	"github.com/example/go-frame/pkg/library/timekit"
	"github.com/example/go-frame/pkg/service/configkit"
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

// logBodyMaxSize 请求参数日志的最大长度（字节），超出截断
const logBodyMaxSize = 1024

// BindForm bean 指针、bean 必须是 struct 定义过的
func (ctx *Context) BindForm(bean any) {
	snapshot := ctx.bindStruct(bean)
	err := Validator.Struct(bean)
	if err != nil {
		if _, ok := err.(*validator.InvalidValidationError); ok {
			panic(exception.New(err.Error()))
		}
		for _, err0 := range err.(validator.ValidationErrors) {
			panic(exception.New("validation failed: " + stringkit.LowerFirst(err0.Field()) + ", need " + err0.Tag()))
		}
	}
	// rest.logRequestBody 开关（默认开）：info 级打印本次请求参数，敏感字段已掩码。
	// 快照取自绑定过程现成的 key=value 对与原始 body 字节，不再对 bean 做反射序列化。
	if snapshot != "" {
		logkit.Info("request-body", "token", ctx.GetToken(), "url", ctx.Request.URL.Path, "body", snapshot)
	}
}

// maskValue 敏感字段值打码：key 含 pwd/password/passwd（不分大小写）即视为密码类参数
func maskValue(key, val string) string {
	lk := strings.ToLower(key)
	if strings.Contains(lk, "pwd") || strings.Contains(lk, "password") || strings.Contains(lk, "passwd") {
		return "******"
	}
	return val
}

// sensitiveJSONRe 掩码原始 JSON body 中字符串型的敏感字段值
var sensitiveJSONRe = regexp.MustCompile(`(?i)"(pwd|password|passwd)"\s*:\s*"[^"]*"`)

func maskJSONBody(body string) string {
	return sensitiveJSONRe.ReplaceAllString(body, `"$1":"******"`)
}

// truncateLogBody 截断到日志长度上限，并清掉截断产生的半截 UTF-8 字符（中文参数常见）
func truncateLogBody(s string) string {
	if len(s) > logBodyMaxSize {
		s = s[:logBodyMaxSize]
	}
	return strings.ToValidUTF8(s, "")
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

// classFileType multipart 文件字段类型，包级缓存避免热路径反复 reflect.TypeOf 构造零值
var classFileType = reflect.TypeOf(class.File{})

// binders A5: 类型 → binder 函数的 map 替代冗长的 type switch，
// 新增类型只需追加一行注册，bindStruct 主体保持简洁。
// key 直接用 reflect.Type（可比较），避免逐字段生成类型名字符串；
// class.File 不在表内，由 bindStruct 的文件分支特判。
var binders = map[reflect.Type]binderFunc{
	reflect.TypeOf(""): func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		fieldV.SetString(val)
	},
	reflect.TypeOf(int(0)):   setIntKind,
	reflect.TypeOf(int8(0)):  setIntKind,
	reflect.TypeOf(int16(0)): setIntKind,
	reflect.TypeOf(int32(0)): setIntKind,
	reflect.TypeOf(int64(0)): setIntKind,
	reflect.TypeOf(byte(0)):  setIntKind,
	reflect.TypeOf(float64(0)): func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if !stringkit.IsNull(val) {
			fieldV.SetFloat(cast.ToFloat64(val))
		}
	},
	reflect.TypeOf(false): func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if !stringkit.IsNull(val) {
			fieldV.SetBool(cast.ToBool(val))
		}
	},
	reflect.TypeOf(class.Int32{}): func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if !stringkit.IsNull(val) {
			fieldV.Set(reflect.ValueOf(class.NewInt32(val)))
		}
	},
	reflect.TypeOf(class.Int64{}): func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if !stringkit.IsNull(val) {
			fieldV.Set(reflect.ValueOf(class.NewInt64(val)))
		}
	},
	reflect.TypeOf(class.Float64{}): func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if !stringkit.IsNull(val) {
			fieldV.Set(reflect.ValueOf(class.NewFloat64(val)))
		}
	},
	reflect.TypeOf(class.Bool{}): func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if !stringkit.IsNull(val) {
			fieldV.Set(reflect.ValueOf(class.NewBool(val)))
		}
	},
	reflect.TypeOf(class.String{}): func(fieldV reflect.Value, val string, keyExist bool, _ reflect.StructField) {
		// 仅当请求中存在该 key 时才赋值，区分"未传"与"传空串"
		if keyExist {
			fieldV.Set(reflect.ValueOf(class.NewString(val)))
		}
	},
	reflect.TypeOf(class.ArrInt{}): func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if stringkit.IsNull(val) {
			return
		}
		var p []int64
		_ = jsonkit.ParseObj(val, &p)
		fieldV.Set(reflect.ValueOf(class.NewArrInt(p)))
	},
	reflect.TypeOf(class.ArrString{}): func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if stringkit.IsNull(val) {
			return
		}
		var p []string
		_ = jsonkit.ParseObj(val, &p)
		fieldV.Set(reflect.ValueOf(class.NewArrString(p)))
	},
	reflect.TypeOf(class.MapString{}): func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if stringkit.IsNull(val) {
			return
		}
		var p map[string]any
		_ = jsonkit.ParseObj(val, &p)
		fieldV.Set(reflect.ValueOf(class.NewMapString(p)))
	},
	reflect.TypeOf(class.MapStringArr{}): func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if stringkit.IsNull(val) {
			return
		}
		var p []map[string]any
		_ = jsonkit.ParseObj(val, &p)
		fieldV.Set(reflect.ValueOf(class.NewMapStringArr(p)))
	},
	reflect.TypeOf(class.Time{}): func(fieldV reflect.Value, val string, _ bool, _ reflect.StructField) {
		if stringkit.IsNull(val) {
			return
		}
		temp := class.Time{}
		if s, err := timekit.Parse(val); err == nil {
			temp.Set(s)
		}
		fieldV.Set(reflect.ValueOf(temp))
	},
	reflect.TypeOf(class.Decimal{}): func(fieldV reflect.Value, val string, _ bool, field reflect.StructField) {
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

// bindStruct 实现 form/query/param 与 json body 的数据绑定，返回请求参数日志快照
// （rest.logRequestBody 关闭或无参数时为空串）。
// comment:"xxx" default:"" trim:"true"
//
// 绑定顺序：先逐字段绑定 form/query/param（json 请求也执行，query/param 因此生效，
// default/trim tag 同样作用于 json 请求），随后 json body 整体覆盖——
// encoding/json 只写 body 中出现的字段，query 与 body 同名字段时 body 优先。
// 取 json 和取 form 只能同时进行一次，取完，流被关闭了。
func (ctx *Context) bindStruct(bean any) string {
	rt0 := reflect.TypeOf(bean)
	if rt0.Kind() != reflect.Pointer {
		panic(exception.New("bindStruct need pointer"))
	}
	rt := rt0.Elem()
	rv := reflect.ValueOf(bean).Elem()
	isJson := strings.Index(ctx.Request.Header.Get("content-type"), "application/json") >= 0
	logBody := configkit.GetBool(configkey.RestLogRequestBody, true)
	var pairs []string
	var rawBody string
	if isJson && logBody {
		// 记录客户端发来的原文，免去对 bean 的反射序列化。
		// gin v1.12 的 GetRawData 读后不回填 body，这里手动回填，ShouldBindJSON 才能再次读取
		if b, err := io.ReadAll(ctx.Request.Body); err == nil && len(b) > 0 {
			ctx.Request.Body = io.NopCloser(bytes.NewBuffer(b))
			rawBody = maskJSONBody(string(b))
		}
	}
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		fieldV := rv.Field(i)
		// B6: json tag 决定参数 key（json:"-" 表示跳过该字段不绑定）
		key, skip := fieldKey(field)
		if skip {
			continue
		}
		// multipart file（json 请求不存在文件字段）
		if field.Type == classFileType {
			if isJson {
				continue
			}
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
				if logBody {
					pairs = append(pairs, key+"="+maskValue(key, file.Filename))
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
		if binder, ok := binders[field.Type]; ok {
			binder(fieldV, val, keyExist, field)
		}
		if logBody && (keyExist || val != "") {
			pairs = append(pairs, key+"="+maskValue(key, val))
		}
	}
	if isJson {
		// P1 修复：原实现 `_ =` 吞掉绑定错误——非法 JSON 会静默变成零值 bean，
		// 与 form 路径（明确报错）行为不一致，必填项靠 validator 兜底、非必填项静默丢失
		if err := ctx.Proxy.ShouldBindJSON(bean); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				panic(exception.New("请求体超过大小限制"))
			}
			panic(exception.New("请求数据解析失败: " + err.Error()))
		}
	}
	if !logBody {
		return ""
	}
	var parts []string
	if len(pairs) > 0 {
		parts = append(parts, strings.Join(pairs, "&"))
	}
	if rawBody != "" {
		parts = append(parts, rawBody)
	}
	if len(parts) == 0 {
		return ""
	}
	return truncateLogBody(strings.Join(parts, " "))
}
