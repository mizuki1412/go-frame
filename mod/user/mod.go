package user

import (
	"github.com/example/go-frame/mod/user/controller/department"
	"github.com/example/go-frame/mod/user/controller/role"
	"github.com/example/go-frame/mod/user/controller/smscode"
	"github.com/example/go-frame/mod/user/controller/user"
	"github.com/example/go-frame/mod/user/service"
	"github.com/example/go-frame/pkg/service/restkit/router"
	"github.com/example/go-frame/pkg/service/tokenkit"
)

// Init 把本模块的权限数据源注入 tokenkit。
//
// 必须在任何鉴权请求到达前调用（main 中放在 AddActions 之前）：
// 未注册数据源时 tokenkit 的权限判定一律拒绝，管理员接口将对所有人关闭。
func Init() {
	tokenkit.SetLoader(service.LoadPrincipal)
}

// All 用户、部门、角色模块
func All() []func(r *router.Router) {
	return []func(r *router.Router){user.Init, role.Init, department.Init, smscode.Init}
}
