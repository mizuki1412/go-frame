package controller

import (
	"path"
	"strings"

	commonmodel "github.com/example/go-frame/mod/common/model"
	"github.com/example/go-frame/mod/common/service"
	"github.com/example/go-frame/mod/user/model"
	"github.com/example/go-frame/pkg/class"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/service/restkit/context"
	"github.com/example/go-frame/pkg/service/restkit/middleware"
	"github.com/example/go-frame/pkg/service/restkit/openapi"
	"github.com/example/go-frame/pkg/service/restkit/router"
)

// uploadParams 上传参数：file 为 multipart 文件字段（BindForm 自动绑定）。
type uploadParams struct {
	File   class.File `comment:"文件"`
	Public bool       `comment:"true 存公共目录（下载免鉴权），false 存私有目录"`
	Folder string     `comment:"common 下子目录，可选"`
}

type uploadRet struct {
	Path string `json:"path" comment:"相对项目目录的路径，用于下载/删除/列表回显"`
}

func Upload(ctx *context.Context) {
	params := uploadParams{}
	ctx.BindForm(&params)
	if params.File.Header == nil {
		panic(exception.New("请选择文件"))
	}
	path := service.Upload(&params.File, params.Public, params.Folder)
	ctx.JsonSuccess(uploadRet{Path: path})
}

// listParams 列表参数：sub 可选，限定子目录（public/private）。
type listParams struct {
	Sub string `comment:"子目录过滤：public/private，空为全部"`
}

func List(ctx *context.Context) {
	params := listParams{}
	ctx.BindForm(&params)
	list := service.List()
	if params.Sub != "" {
		filtered := make([]commonmodel.FileInfo, 0, len(list))
		for _, f := range list {
			if strings.Contains(f.Path, params.Sub) {
				filtered = append(filtered, f)
			}
		}
		list = filtered
	}
	ctx.JsonSuccess(list)
}

type delParams struct {
	Path string `validate:"required" comment:"列表接口返回的 path"`
}

func Del(ctx *context.Context) {
	params := delParams{}
	ctx.BindForm(&params)
	service.Delete(params.Path)
	ctx.JsonSuccess()
}

type downloadParams struct {
	Path string `validate:"required" comment:"相对项目目录的路径"`
	Name string `comment:"下载显示的文件名，默认取路径末段"`
}

func Download(ctx *context.Context) {
	params := downloadParams{}
	ctx.BindForm(&params)
	full := service.Resolve(params.Path)
	name := params.Name
	if name == "" {
		name = path.Base(params.Path)
	}
	// FileDirect 拼接 obsolutePath+name，这里传目录（含尾斜杠）与空文件名
	ctx.SetFileHeader(name)
	ctx.FileDirect(full, "")
}

func Init(r *router.Router) {
	tag := "common:通用模块"
	// 上传/列表须登录；删除额外收敛到 common:file 权限
	ra := r.Group("/common", middleware.AuthLogin())
	{
		ra.Post("/upload", Upload).Api(openapi.Tag(tag), openapi.Summary("上传文件"),
			openapi.ReqBody(uploadParams{}), openapi.Response(uploadRet{}))
		ra.Post("/file/list", List).Api(openapi.Tag(tag), openapi.Summary("文件列表"),
			openapi.ReqParam(listParams{}), openapi.Response([]commonmodel.FileInfo{}))
	}
	rd := r.Group("/common", middleware.AuthPerm(model.PermCommonFile))
	{
		rd.Post("/file/del", Del).Api(openapi.Tag(tag), openapi.Summary("删除文件"),
			openapi.ReqParam(delParams{}))
	}
	// 下载：private 须登录，public 免鉴权（富文本图片等 <img> 标签无法带 Authorization 头）
	r.Group("/common/download/private", middleware.AuthLogin()).Get("", Download).Api(openapi.Tag(tag),
		openapi.Summary("私有下载"), openapi.ReqParam(downloadParams{}), openapi.Security(nil))
	r.Group("/common/download").Get("", Download).Api(openapi.Tag(tag),
		openapi.Summary("公共下载"), openapi.ReqParam(downloadParams{}), openapi.Security(nil))
}
