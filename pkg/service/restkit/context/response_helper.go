package context

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/example/go-frame/pkg/service/logkit"
	"github.com/example/go-frame/pkg/service/storagekit"
	"github.com/gin-gonic/gin/render"
)

type RestRet struct {
	Result  int    `json:"result" comment:"成功为0，授权拦截为401，错误为500"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty" comment:"数据" data:"true"`
	Total   uint64 `json:"total,omitempty" comment:"记录总数，如果data是列表并且分页"`
}

const ResultErr = 500
const ResultSuccess = 0
const ResultAuthErr = 401

// TransferRestRet 用于自定义返回结构时的转换
var TransferRestRet = func(ret RestRet) any {
	return ret
}

// Json http返回json数据
func (ctx *Context) Json(ret RestRet) {
	var code int
	switch ret.Result {
	case ResultSuccess:
		code = http.StatusOK
	case ResultAuthErr:
		code = http.StatusUnauthorized
	default:
		code = http.StatusInternalServerError
	}
	ctx.Proxy.JSON(code, TransferRestRet(ret))
}

func (ctx *Context) JsonSuccess(data ...any) {
	var d any = nil
	if len(data) > 0 {
		d = data[0]
	}
	ctx.Json(RestRet{
		Result: ResultSuccess,
		Data:   d,
	})
}

func (ctx *Context) RawSuccess(data []byte) {
	ctx.Proxy.Render(http.StatusOK, render.Data{Data: data})
}

func (ctx *Context) Html(data []byte) {
	ctx.Proxy.Render(http.StatusOK, render.Data{Data: data, ContentType: "text/html"})
}

// JsonSuccessWithPage 带分页信息
func (ctx *Context) JsonSuccessWithPage(data any, total uint64) {
	ret := RestRet{
		Result: ResultSuccess,
		Data:   data,
		Total:  total,
	}
	ctx.Json(ret)
}
func (ctx *Context) JsonError(msg string) {
	ctx.Json(RestRet{
		Result:  ResultErr,
		Message: msg,
	})
}

// JsonErrorCode 业务错误码响应：code 原样透传到 result（300~999 业务段 / 401 鉴权）。
func (ctx *Context) JsonErrorCode(code int, msg string) {
	ctx.Json(RestRet{
		Result:  code,
		Message: msg,
	})
}

func (ctx *Context) SetFileHeader(filename string) {
	// RFC 5987/6266：非 ASCII 文件名用 filename* 透传 UTF-8，filename 给不识别该
	// 语法的旧客户端回退。原先用 url.QueryEscape 有两个问题：空格被编码成 '+'，
	// 浏览器下载的文件名会真的显示加号；中文名依赖单字段转义，兼容性差。
	fallback := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, filename)
	if fallback == "" {
		fallback = "download"
	}
	ctx.Proxy.Header("Content-Disposition",
		"attachment; filename="+strconv.Quote(fallback)+"; filename*=UTF-8''"+url.PathEscape(filename))
	ctx.Proxy.Header("Content-Type", "application/octet-stream")
	ctx.Proxy.Header("Content-Transfer-Encoding", "binary")
	ctx.Proxy.Header("Pragma", "No-cache")
	ctx.Proxy.Header("Cache-Control", "No-cache")
	ctx.Proxy.Header("Expires", "0")
}
func (ctx *Context) SetJsonHeader() {
	ctx.Proxy.Header("Content-Type", "application/json")
}

func (ctx *Context) FileRaw(data []byte, name string) {
	ctx.SetFileHeader(name)
	ctx.RawSuccess(data)
}

// File 相对于项目目录路径的
func (ctx *Context) File(relativePath, name string) {
	ctx.FileDirect(storagekit.GetFullPath(relativePath), name)
}
func (ctx *Context) File2(relativePathName string) {
	ctx.Proxy.File(storagekit.GetFullPath(relativePathName))
}

func (ctx *Context) FileDirect(obsolutePath, name string) {
	ctx.Proxy.File(obsolutePath + name)
}

// SendSSE 发送一条 SSE 事件帧。msg 含换行时按 SSE 规范拆成多个 data: 行——
// 帧内不允许裸换行，原样写入会把一条消息撕成多条残缺事件。
func (ctx *Context) SendSSE(msg string) {
	ctx.Proxy.Header("Content-Type", "text/event-stream")
	ctx.Proxy.Header("Cache-Control", "no-cache")
	ctx.Proxy.Header("Connection", "keep-alive")
	var b strings.Builder
	b.WriteString("event: message\n")
	for _, line := range strings.Split(strings.ReplaceAll(msg, "\r\n", "\n"), "\n") {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	_, err := ctx.Proxy.Writer.WriteString(b.String())
	if err != nil {
		logkit.Error(err.Error())
		return
	}
	ctx.Proxy.Writer.Flush()
}
