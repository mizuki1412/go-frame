package user

import (
	"strconv"
	"time"

	"github.com/example/go-frame/mod/user/model"
	"github.com/example/go-frame/mod/user/service"
	"github.com/example/go-frame/pkg/class"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/service/restkit/context"
	"github.com/example/go-frame/pkg/service/tokenkit"
)

// ScopeApp 业务端 token 域。与管理端共用同一套会话存储，靠 scope 隔离，
// 业务端 token 不能横向调用 /user/admin、/role、/department。
const ScopeApp = "app"

type loginByUsernameParam struct {
	Username string `comment:"用户名" validate:"required"`
	Pwd      string `validate:"required"`
}

type ResLogin struct {
	User  *model.User `json:"user"`
	Token string      `json:"token"`
}

func LoginByUsername(ctx *context.Context) {
	params := loginByUsernameParam{}
	ctx.BindForm(&params)
	user := service.Login(params.Username, "", params.Pwd)
	ret := issueToken(ctx, user)
	if AdditionLoginFunc != nil {
		AdditionLoginFunc(ctx, ret)
	}
	ctx.JsonSuccess(ret)
}

type loginParam struct {
	Username string `comment:"用户名"`
	Phone    string `comment:"手机号"`
	Pwd      string `validate:"required"`
}

// Login 通用登录（用户名或手机号）
func Login(ctx *context.Context) {
	params := loginParam{}
	ctx.BindForm(&params)
	user := service.Login(params.Username, params.Phone, params.Pwd)
	ret := issueToken(ctx, user)
	if AdditionLoginFunc != nil {
		AdditionLoginFunc(ctx, ret)
	}
	ctx.JsonSuccess(ret)
}

// issueToken 签发会话并写 cookie。
// token 由服务端随机生成、不含载荷，注销与剔出都靠删服务端会话即时生效。
func issueToken(ctx *context.Context, user *model.User) ResLogin {
	token := tokenkit.Create(strconv.FormatInt(user.Id, 10), ctx.SessionOptions(ScopeApp)...)
	ctx.SetTokenCookie(token, time.Now().Add(tokenkit.ExpireTtl()))
	return ResLogin{User: user, Token: token}
}

var AdditionLoginFunc func(ctx *context.Context, ret ResLogin)

var AdditionUserExFunc func(ctx *context.Context, u *model.User)

var AdditionUserInfoWithIdFunc = func(ctx *context.Context, u *model.User) {
	// 默认不支持普通用户获取其他用户信息
	panic(exception.New("无权限获取用户信息"))
}

type infoParam struct {
	Id class.Int64 `comment:"不填获取自己，并且返回的是user和token；否则只返回user"`
}

func Info(ctx *context.Context) {
	params := infoParam{}
	ctx.BindForm(&params)
	if !params.Id.Valid {
		// 获取自己的
		uid := ctx.GetUid()
		user := service.GetUserById(uid)
		if user == nil {
			panic(exception.New("用户不存在"))
		}
		// 续期当前会话而非重新签发：token 不透明，重签会让同一浏览器堆积旧会话
		ctx.RefreshToken()
		ret := ResLogin{
			User:  user,
			Token: ctx.GetToken(),
		}
		if AdditionUserExFunc != nil {
			AdditionUserExFunc(ctx, user)
		}
		ctx.JsonSuccess(ret)
	} else {
		user := service.GetUserById(params.Id.Int64)
		if user == nil {
			panic(exception.New("无此用户"))
		}
		if AdditionUserExFunc != nil {
			AdditionUserExFunc(ctx, user)
		}
		AdditionUserInfoWithIdFunc(ctx, user)
		ctx.JsonSuccess(user)
	}
}

func Logout(ctx *context.Context) {
	ctx.DestroyToken()
	ctx.JsonSuccess()
}

type updatePwdParam struct {
	OldPwd string `validate:"required"`
	NewPwd string `validate:"required"`
}

func UpdatePwd(ctx *context.Context) {
	params := updatePwdParam{}
	ctx.BindForm(&params)
	uid := ctx.GetUid()
	service.UpdatePwd(uid, params.OldPwd, params.NewPwd)
	ctx.JsonSuccess()
}

type updateUserInfoParam = service.UpdateUserInfoParams

func UpdateUserInfo(ctx *context.Context) {
	params := updateUserInfoParam{}
	ctx.BindForm(&params)
	uid := ctx.GetUid()
	service.UpdateUserInfo(uid, params)
	ctx.JsonSuccess()
}
