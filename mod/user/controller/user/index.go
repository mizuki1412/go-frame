package user

import (
	"github.com/example/go-frame/mod/user/model"
	"github.com/example/go-frame/mod/user/service"
	"github.com/example/go-frame/pkg/service/restkit/middleware"
	"github.com/example/go-frame/pkg/service/restkit/openapi"
	"github.com/example/go-frame/pkg/service/restkit/router"
)

func Init(router *router.Router) {
	tag := "user:用户模块"
	router.Group("/user/loginByUsername").Post("", LoginByUsername).Api(openapi.Tag(tag),
		openapi.Summary("登录-用户名"),
		openapi.ReqParam(loginByUsernameParam{}), openapi.Response(ResLogin{}),
		openapi.Security(nil))
	router.Group("/user/login").Post("", Login).Api(openapi.Tag(tag),
		openapi.Summary("登录"),
		openapi.ReqParam(loginParam{}), openapi.Response(ResLogin{}),
		openapi.Security(nil))
	router.Group("/user/info").Use(middleware.AuthLogin()).Get("", Info).Api(openapi.Tag(tag),
		openapi.Summary("用户信息，续期token"))

	// 业务端：只需登录态
	r := router.Group("/user", middleware.AuthLogin())
	{
		// P2 修复：logout/del 为状态变更操作，原走 GET（语义错误、易被预取/缓存/日志采集误触发），改 POST。
		// BindForm 合并 PostForm/Query/Param，原有 query 传参方式依然兼容
		r.Post("/logout", Logout).Api(openapi.Tag(tag), openapi.Summary("登出"))
		r.Post("/updatePwd", UpdatePwd).Api(openapi.Tag(tag), openapi.Summary("密码修改"), openapi.ReqParam(updatePwdParam{}))
		r.Post("/updateUserInfo", UpdateUserInfo).Api(openapi.Tag(tag), openapi.Summary("更新用户信息"), openapi.ReqBody(updateUserInfoParam{}))
	}

	// 用户管理：需 user:admin 权限
	r1 := router.Group("/user/admin", middleware.AuthPerm(model.PermUserAdmin))
	{
		r1.Post("/list", ListUsers).Api(openapi.Tag(tag),
			openapi.Summary("用户列表"), openapi.ReqBody(listUsersParams{}), openapi.Response([]*model.User{}))
		r1.Get("/info", InfoAdmin).Api(openapi.Tag(tag),
			openapi.Summary("用户信息"), openapi.ReqParam(infoAdminParams{}), openapi.Response(model.User{}))
	}
	r2 := router.Group("/user/admin", middleware.AuthPerm(model.PermUserAdmin))
	{
		r2.Post("/add", AddUser).Api(openapi.Tag(tag), openapi.Summary("添加用户"), openapi.ReqBody(AddUserParams{}))
		r2.Post("/update", UpdateUser).Api(openapi.Tag(tag), openapi.Summary("修改用户"), openapi.ReqBody(UpdateParams{}))
		// P2 修复：删除是状态变更操作，GET → POST
		r2.Post("/del", DeleteUser).Api(openapi.Tag(tag), openapi.Summary("删除冻结用户"), openapi.ReqParam(DelParams{}))
	}

	// 在线会话管理：需 user:session 权限（与用户管理分开，便于把「能管用户」和「能踢人」拆开授权）
	rs := router.Group("/user/admin/session", middleware.AuthPerm(model.PermUserSession))
	{
		rs.Post("/list", ListOnlineSessions).Api(openapi.Tag(tag),
			openapi.Summary("在线用户列表"), openapi.ReqParam(listOnlineParams{}), openapi.Response([]service.OnlineSession{}))
		rs.Post("/listByUser", ListUserSessions).Api(openapi.Tag(tag),
			openapi.Summary("指定用户的在线会话"), openapi.ReqParam(listUserSessionsParams{}))
		rs.Post("/kickoutUser", KickoutUser).Api(openapi.Tag(tag),
			openapi.Summary("剔出用户（全部会话）"), openapi.ReqParam(kickoutUserParams{}))
		rs.Post("/kickoutToken", KickoutToken).Api(openapi.Tag(tag),
			openapi.Summary("剔出单个会话"), openapi.ReqParam(kickoutTokenParams{}))
	}
}
