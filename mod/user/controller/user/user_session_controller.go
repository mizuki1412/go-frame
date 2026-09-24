package user

import (
	"github.com/example/go-frame/mod/user/service"
	"github.com/example/go-frame/pkg/service/restkit/context"
)

type listOnlineParams struct {
	Offset int `comment:"偏移量"`
	Limit  int `comment:"每页条数，最大 200；0 表示只取总数"`
}

// ListOnlineSessions 在线用户列表（全局，按最近活跃时间倒序）。
func ListOnlineSessions(ctx *context.Context) {
	params := listOnlineParams{}
	ctx.BindForm(&params)
	if params.Limit < 0 {
		params.Limit = 0
	}
	if params.Limit > 200 {
		params.Limit = 200
	}
	list, total := service.ListOnlineSessions(params.Offset, params.Limit)
	ctx.JsonSuccessWithPage(list, uint64(total))
}

type listUserSessionsParams struct {
	Uid int64 `validate:"required" comment:"用户id"`
}

// ListUserSessions 指定用户的在线会话列表。
func ListUserSessions(ctx *context.Context) {
	params := listUserSessionsParams{}
	ctx.BindForm(&params)
	ctx.JsonSuccess(service.ListUserSessions(params.Uid))
}

type kickoutUserParams struct {
	Uid int64 `validate:"required" comment:"被剔出的用户id"`
}

// KickoutUser 剔出用户全部在线会话。
func KickoutUser(ctx *context.Context) {
	params := kickoutUserParams{}
	ctx.BindForm(&params)
	n := service.KickoutUser(ctx.GetUid(), params.Uid)
	ctx.JsonSuccess(n)
}

type kickoutTokenParams struct {
	Token string `validate:"required" comment:"被剔出的会话 token"`
}

// KickoutToken 强制下线单个会话。
func KickoutToken(ctx *context.Context) {
	params := kickoutTokenParams{}
	ctx.BindForm(&params)
	service.KickoutToken(params.Token)
	ctx.JsonSuccess()
}
