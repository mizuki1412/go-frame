package openapi

import (
	"net/http"
	"reflect"
	"strings"

	"github.com/example/go-frame/pkg/class/const/httpconst"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/cli/tag"
	"github.com/example/go-frame/pkg/library/jsonkit"
	"github.com/example/go-frame/pkg/library/stringkit"
	"github.com/example/go-frame/pkg/service/configkit"
	"github.com/example/go-frame/pkg/service/logkit"
	"github.com/example/go-frame/pkg/service/restkit/context"
)

var Doc *ApiDocV3

func init() {
	Doc = &ApiDocV3{
		Paths: map[string]map[string]*ApiDocV3PathOperation{},
		Components: &ApiDocV3ComponentObj{
			Schemas:         map[string]*ApiDocV3Schema{},
			SecuritySchemes: map[string]*ApiDocV3SecurityScheme{},
			Responses:       map[string]*ApiDocV3ResBody{},
			Parameters:      map[string]*ApiDocV3ReqParam{},
			Examples:        map[string]*ApiDocV3Example{},
		},
	}
	InitResParentSchema(context.RestRet{})
	// 注册默认 JWT Bearer 安全方案
	Doc.Components.SecuritySchemes[SecuritySchemeBearer] = &ApiDocV3SecurityScheme{
		Type:         "http",
		Scheme:       "bearer",
		BearerFormat: "JWT",
		Description:  "JWT Bearer Token。通过 /user/loginByUsername 或 /user/login 获取 token，置于 Authorization: Bearer <token>。",
	}
	// 默认所有 operation 需要 JWT（controller 可通过 Security(nil) 覆盖）
	Doc.Security = []map[string][]string{
		{SecuritySchemeBearer: {}},
	}
}

// Builder 单条路径的builder
type Builder struct {
	Path *ApiDocV3PathOperation
}

// BuildOpt 定义Functional Options
type BuildOpt func(*Builder)

// GenOperationId path转首字母大写后拼接，同时把其中的路径参数标识出来。
// A13: 统一路径参数格式为 OpenAPI 规范的 {name}：
//   - gin 单段参数 `:id` → `{id}`
//   - gin catch-all `*action` → `{action}`（OpenAPI 不区分单段/多段，统一为 {name}）
//   - 已是 `{name}` 形式的保持不变
//
// 替换按路径段逐段进行：原实现用 strings.ReplaceAll(path, ":id", "{id}")，
// 参数名互为前缀时（/:id 与 /:id2）会把 ":id2" 污染成 "{id}2"，生成非法 path。
func GenOperationId(path, method string) (string, string) {
	res := strings.ToLower(method)
	segs := strings.Split(path, "/")
	for i, e := range segs {
		if e == "" {
			continue
		}
		// gin 路径参数 :id → {id}；catch-all *action → {action}
		if e[0] == ':' || e[0] == '*' {
			segs[i] = "{" + e[1:] + "}"
			continue
		}
		// 跳过路径参数段（已转为 {name} 或原本就是 {name}），不参与 operationId 拼接
		if e[0] == '{' {
			continue
		}
		res += stringkit.UpperFirst(e)
	}
	return res, strings.Join(segs, "/")
}

// NewBuilder 构建单条路径的 operation。
// 响应默认包含 200（用 RestRet 作为父 schema）+ 400/401/500 错误响应。
func NewBuilder(path string, method string) *Builder {
	op := &ApiDocV3PathOperation{}
	// 默认 200 响应：RestRet 父结构 + data 字段
	op.Responses = defaultResponses()
	op.OperationId, path = GenOperationId(path, method)
	if _, ok := Doc.Paths[path]; !ok {
		Doc.Paths[path] = map[string]*ApiDocV3PathOperation{}
	}
	// gin 对重复路由会 panic，正常到不了这里；但文档侧静默覆盖会掩盖问题，显式告警
	if _, exists := Doc.Paths[path][method]; exists {
		logkit.Info("openapi 路径重复注册，后者覆盖前者", "path", path, "method", method)
	}
	Doc.Paths[path][method] = op
	return &Builder{Path: op}
}

// defaultResponses 构造默认响应 map：200 + 400 + 401 + 500。
// 200 用 RestRet 父 schema；4xx/5xx 用 $ref 引用 components.responses 中预注册的复用响应。
func defaultResponses() map[string]*ApiDocV3ResBody {
	parent := newRestRetParentSchema()
	responses := map[string]*ApiDocV3ResBody{
		"200": {
			Description: "成功",
			Content: map[string]*ApiDocV3SchemaWrapper{
				httpconst.MimeJSON: {Schema: parent},
			},
		},
	}
	// 4xx/5xx 用 $ref 引用 components.responses，避免多个 operation 共享同一指针
	for code := range Doc.Components.Responses {
		responses[code] = &ApiDocV3ResBody{Ref: "#/components/responses/" + code}
	}
	return responses
}

// newRestRetParentSchema 构造 RestRet 父 schema（深拷贝，避免多个 operation 共享同一指针导致互相污染）。
func newRestRetParentSchema() *ApiDocV3Schema {
	parent := &ApiDocV3Schema{}
	if err := jsonkit.ParseObj(resParentSchema, parent); err != nil {
		panic(exception.New(err.Error()))
	}
	// data 字段默认 object，由 Response() 覆盖为具体类型/数组
	if parent.Properties == nil {
		parent.Properties = map[string]*ApiDocV3Schema{}
	}
	if parent.Properties[resParentSchemaDataKey] == nil {
		parent.Properties[resParentSchemaDataKey] = &ApiDocV3Schema{}
	}
	parent.Properties[resParentSchemaDataKey].Type = SchemaTypeObject
	return parent
}

// registerDefaultErrorResponses 预注册 400/401/500 响应到 components.responses，便于复用。
func registerDefaultErrorResponses() {
	for code, desc := range map[string]string{
		"400": "请求参数错误",
		"401": "未授权或登录失效",
		"500": "服务器内部错误",
	} {
		parent := &ApiDocV3Schema{Properties: map[string]*ApiDocV3Schema{}}
		parent.Type = SchemaTypeObject
		// 复用 RestRet 结构作为错误响应体（result/message 字段）
		if err := jsonkit.ParseObj(resParentSchema, parent); err == nil {
			// data 字段在错误场景下一般为空，移除
			delete(parent.Properties, resParentSchemaDataKey)
		}
		Doc.Components.Responses[code] = &ApiDocV3ResBody{
			Description: desc,
			Content: map[string]*ApiDocV3SchemaWrapper{
				httpconst.MimeJSON: {Schema: parent},
			},
		}
	}
}

// ==================== Operation-level Options ====================

func Description(val string) BuildOpt {
	return func(b *Builder) {
		b.Path.Description = val
	}
}

func Summary(val string) BuildOpt {
	return func(b *Builder) {
		b.Path.Summary = val
	}
}

// Tag 设置 operation 所属 tag，并同步到 Doc.Tags。
func Tag(tagName string) BuildOpt {
	return func(b *Builder) {
		b.Path.Tags = []string{tagName}
		for _, e := range Doc.Tags {
			if e.Name == tagName {
				return
			}
		}
		Doc.Tags = append(Doc.Tags, &ApiDocV3Tag{
			Name:        tagName,
			Description: tagName,
		})
	}
}

// Deprecated 标记当前 operation 已弃用。
func Deprecated() BuildOpt {
	return func(b *Builder) {
		b.Path.Deprecated = true
	}
}

// OperationId 显式指定 operationId。
func OperationId(id string) BuildOpt {
	return func(b *Builder) {
		b.Path.OperationId = id
	}
}

// Security 设置 operation 级安全要求。
// 传 nil 表示该 operation 不鉴权（如 /login）；传空 map 列表表示使用顶层默认。
func Security(requirements []map[string][]string) BuildOpt {
	return func(b *Builder) {
		b.Path.Security = requirements
	}
}

// ExternalDocs 设置 operation 级外部文档链接。
func ExternalDocs(description, url string) BuildOpt {
	return func(b *Builder) {
		// operation 级 ExternalDocs 未在 ApiDocV3PathOperation 中定义，跳过
		// 如需支持，可在 ApiDocV3PathOperation 上加 ExternalDocs 字段
	}
}

// ==================== Request ====================

// ReqParam 声明查询/路径/头/cookie 参数。
// param 为 struct，每个字段的 tag：
//   - comment: 描述
//   - validate: required
//   - default: 默认值
//   - in: query/path/header/cookie（默认 query）
//   - schema: ignore 跳过
//   - example: 示例
//   - deprecated: 弃用
//   - allowEmptyValue: 允许空值（仅 query）
func ReqParam(param any) BuildOpt {
	return func(b *Builder) {
		rt := reflect.TypeOf(param)
		buildFieldSchemas(rt, func(s *ApiDocV3Schema, field reflect.StructField, name string) {
			if s.Format == SchemaFormatBinary {
				panic(exception.New("file请使用reqBody"))
			}
			b.Path.Parameters = append(b.Path.Parameters, buildReqParamElement(s, field, name))
		})
	}
}

// ReqBody 声明请求体。
// 支持 application/json 和 multipart/form-data（含 class.File 字段时自动切换）。
func ReqBody(param any) BuildOpt {
	return func(b *Builder) {
		b.Path.RequestBody = &ApiDocV3ReqBody{Content: map[string]*ApiDocV3SchemaWrapper{}}
		keyList := []string{httpconst.MimeJSON}
		rt := reflect.TypeOf(param)
		schema := &ApiDocV3Schema{Properties: map[string]*ApiDocV3Schema{}}
		schema.Type = SchemaTypeObject
		// A14: name 由 buildFieldSchemas 传入，避免回调中重复解析
		buildFieldSchemas(rt, func(s *ApiDocV3Schema, field reflect.StructField, name string) {
			// file 类型触发 multipart
			if s.Format == SchemaFormatBinary {
				keyList[0] = httpconst.MimeMultipartPOSTForm
			}
			// 存在 in tag 的字段加入 parameters
			if tag.ParamIn.Exist(field.Tag) {
				b.Path.Parameters = append(b.Path.Parameters, buildReqParamElement(s, field, name))
				return
			}
			if tag.Validate.Contain(field.Tag, tag.ValidateRequired) {
				schema.Required = append(schema.Required, name)
			}
			schema.Properties[name] = s
		})
		b.Path.RequestBody.Content[keyList[0]] = &ApiDocV3SchemaWrapper{Schema: schema}
	}
}

// ==================== Response ====================

// Response 声明 200 响应的具体 data schema。
// bean 为 struct 时 data 用 $ref 引用 components.schemas；为 slice 时 data 为 array。
func Response(bean any) BuildOpt {
	return func(b *Builder) {
		rt := reflect.TypeOf(bean)
		parent := newRestRetParentSchema()
		if rt.Kind() == reflect.Slice {
			parent.Properties[resParentSchemaDataKey].Type = SchemaTypeArray
			parent.Properties[resParentSchemaDataKey].Items = buildSchemaByType(rt.Elem())
		} else {
			ref := buildComponentSchema(rt)
			parent.Properties[resParentSchemaDataKey].Type = SchemaTypeObject
			parent.Properties[resParentSchemaDataKey].Ref = ref
		}
		// 保留预注册的 4xx/5xx 响应
		responses := defaultResponses()
		responses["200"] = &ApiDocV3ResBody{
			Description: "成功",
			Content: map[string]*ApiDocV3SchemaWrapper{
				httpconst.MimeJSON: {Schema: parent},
			},
		}
		b.Path.Responses = responses
	}
}

// ResponseStream 返回字节流。
func ResponseStream() BuildOpt {
	return func(b *Builder) {
		b.Path.Responses = map[string]*ApiDocV3ResBody{
			"200": {
				Description: "字节流",
				Content: map[string]*ApiDocV3SchemaWrapper{
					httpconst.MimeStream: {Schema: &ApiDocV3Schema{Type: SchemaTypeString, Format: SchemaFormatBinary}},
				},
			},
		}
	}
}

// ==================== Doc-level ====================

// AddServer 添加服务端点。多环境时多次调用。
func AddServer(url, description string) {
	Doc.Servers = append(Doc.Servers, &ApiDocV3Server{
		Url:         url,
		Description: description,
	})
}

// SetExternalDocs 设置顶层外部文档链接。
func SetExternalDocs(description, url string) {
	Doc.ExternalDocs = &ApiDocV3ExternalDocs{
		Description: description,
		Url:         url,
	}
}

// AddSecurityScheme 添加自定义安全方案。
func AddSecurityScheme(name string, scheme *ApiDocV3SecurityScheme) {
	if Doc.Components.SecuritySchemes == nil {
		Doc.Components.SecuritySchemes = map[string]*ApiDocV3SecurityScheme{}
	}
	Doc.Components.SecuritySchemes[name] = scheme
}

// AddExample 添加复用示例。
func AddExample(name string, example *ApiDocV3Example) {
	if Doc.Components.Examples == nil {
		Doc.Components.Examples = map[string]*ApiDocV3Example{}
	}
	Doc.Components.Examples[name] = example
}

// AddParameter 添加复用参数。
func AddParameter(name string, param *ApiDocV3ReqParam) {
	if Doc.Components.Parameters == nil {
		Doc.Components.Parameters = map[string]*ApiDocV3ReqParam{}
	}
	Doc.Components.Parameters[name] = param
}

// ReadDoc 返回 api-docs 结果，补全 Info/Servers 等。
func (doc *ApiDocV3) ReadDoc() *ApiDocV3 {
	doc.Openapi = "3.1.0"
	if doc.Info == nil {
		doc.Info = &ApiDocV3Info{
			Title:       configkit.GetString(configkey.OpenApiTitle),
			Description: configkit.GetString(configkey.OpenApiDescription),
			Version:     configkit.GetString(configkey.OpenApiVersion),
		}
		if configkit.Exist(configkey.OpenApiContactEmail) || configkit.Exist(configkey.OpenApiContactName) || configkit.Exist(configkey.OpenApiContactUrl) {
			doc.Info.Contact = &ApiDocV3InfoContact{
				Name:  configkit.GetString(configkey.OpenApiContactName),
				Url:   configkit.GetString(configkey.OpenApiContactUrl),
				Email: configkit.GetString(configkey.OpenApiContactEmail),
			}
		}
	}
	// 默认 Server：从 config 或 / 推断
	if len(doc.Servers) == 0 {
		doc.Servers = []*ApiDocV3Server{
			{Url: "/", Description: "当前服务"},
		}
	}
	return doc
}

// ==================== 内部：RestRet 父 schema ====================

var resParentSchema string
var resParentSchemaDataKey string

// InitResParentSchema 解析 RestRet 结构，生成响应外层 schema 模板字符串。
// data 字段由 Response() 动态填充。
func InitResParentSchema(obj any) {
	rt := reflect.TypeOf(obj)
	for i := 0; i < rt.NumField(); i++ {
		if tag.RetData.Hit(rt.Field(i).Tag) {
			resParentSchemaDataKey = stringkit.LowerFirst(rt.Field(i).Name)
			break
		}
	}
	if resParentSchemaDataKey == "" {
		panic(exception.New("resParentSchemaDataKey cannot nil"))
	}
	s := buildObjectSchema(rt)
	resParentSchema = jsonkit.ToString(s)
	// 预注册默认错误响应
	registerDefaultErrorResponses()
}

// 兼容旧代码引用 http.StatusXXX 常量
var _ = http.StatusOK
