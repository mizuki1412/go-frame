package role

import (
	"github.com/example/go-frame/mod/user/model"
	"github.com/example/go-frame/pkg/service/restkit/middleware"
	"github.com/example/go-frame/pkg/service/restkit/openapi"
	"github.com/example/go-frame/pkg/service/restkit/router"
)

func Init(router *router.Router) {
	tag := "role:用户模块-角色管理"
	r := router.Group("/role").Use(middleware.AuthJWT())
	r.Get("/privilege/list", ListAllPrivileges).Api(openapi.Tag(tag), openapi.Summary("所有权限列表"),
		openapi.Response([]*model.PrivilegeConstant{}))
	r.Post("/list", ListRoles).Api(openapi.Tag(tag), openapi.Summary("role列表"),
		openapi.Response([]*model.Role{}))
	r.Post("/create", CreateRole).Api(openapi.Tag(tag), openapi.Summary("role新增"), openapi.ReqBody(createParams{}))
	r.Post("/update", UpdateRole).Api(openapi.Tag(tag), openapi.Summary("role修改"), openapi.ReqBody(updateParams{}))
	r.Get("/del", DeleteRole).Api(openapi.Tag(tag), openapi.Summary("role删除"), openapi.ReqParam(delParams{}))
	r.Post("/listRolesWithUser", ListRolesWithUser).Api(openapi.Tag(tag),
		openapi.Summary("列出所有角色，附带所属users"),
		openapi.ReqParam(listByRoleParams{}), openapi.Response([]*model.Role{}))
}
