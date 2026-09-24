package role

import (
	"github.com/example/go-frame/mod/user/service"
	"github.com/example/go-frame/pkg/service/restkit/context"
)

func ListAllPrivileges(ctx *context.Context) {
	ctx.JsonSuccess(service.ListAllPrivileges())
}

type createParams = service.CreateRoleParams

func CreateRole(ctx *context.Context) {
	params := createParams{}
	ctx.BindForm(&params)
	service.CreateRole(params)
	ctx.JsonSuccess()
}

type updateParams = service.UpdateRoleParams

func UpdateRole(ctx *context.Context) {
	params := updateParams{}
	ctx.BindForm(&params)
	service.UpdateRole(params)
	ctx.JsonSuccess()
}

type delParams = service.DeleteRoleParams

func DeleteRole(ctx *context.Context) {
	params := delParams{}
	ctx.BindForm(&params)
	service.DeleteRole(params.Id)
	ctx.JsonSuccess()
}

func ListRoles(ctx *context.Context) {
	ctx.JsonSuccess(service.ListRoles())
}

type listByRoleParams = service.ListRolesWithUserParams

func ListRolesWithUser(ctx *context.Context) {
	params := listByRoleParams{}
	ctx.BindForm(&params)
	ctx.JsonSuccess(service.ListRolesWithUser(params))
}
