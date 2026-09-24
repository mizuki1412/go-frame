package user

import (
	"github.com/example/go-frame/mod/user/model"
	"github.com/example/go-frame/mod/user/service"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/service/restkit/context"
)

var AdditionUserExAdminFunc func(ctx *context.Context, u *model.User)

type listUsersParams = service.ListUsersParams

func ListUsers(ctx *context.Context) {
	params := listUsersParams{}
	ctx.BindForm(&params)
	list := service.ListUsers(params)
	if AdditionUserExAdminFunc != nil {
		for _, u := range list {
			AdditionUserExAdminFunc(ctx, u)
		}
	}
	ctx.JsonSuccess(list)
}

type AddUserParams = service.AddUserParams

func AddUser(ctx *context.Context) {
	params := AddUserParams{}
	ctx.BindForm(&params)
	u := service.AddUser(params, false)
	ctx.JsonSuccess(u)
}

type UpdateParams = service.UpdateUserParams

func UpdateUser(ctx *context.Context) {
	params := UpdateParams{}
	ctx.BindForm(&params)
	service.UpdateUser(params)
	ctx.JsonSuccess()
}

type infoAdminParams struct {
	Uid int64 `validate:"required"`
}

func InfoAdmin(ctx *context.Context) {
	params := infoAdminParams{}
	ctx.BindForm(&params)
	user := service.GetUserById(params.Uid)
	if user == nil {
		panic(exception.New("无此用户"))
	}
	ctx.JsonSuccess(user)
}

type DelParams = service.DeleteUserParams

func DeleteUser(ctx *context.Context) {
	params := DelParams{}
	ctx.BindForm(&params)
	operatorUid := ctx.GetUid()
	service.DeleteUser(operatorUid, params)
	ctx.JsonSuccess()
}
