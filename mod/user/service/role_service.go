package service

import (
	"time"

	"github.com/example/go-frame/mod/user/dao/privilegedao"
	"github.com/example/go-frame/mod/user/dao/roledao"
	"github.com/example/go-frame/mod/user/dao/userdao"
	"github.com/example/go-frame/mod/user/dao/userroledao"
	"github.com/example/go-frame/mod/user/model"
	"github.com/example/go-frame/pkg/class"
	"github.com/example/go-frame/pkg/class/exception"
)

func ListAllPrivileges() []*model.PrivilegeConstant {
	dao := privilegedao.New(privilegedao.OptsNone)
	return dao.ListPrivileges()
}

type CreateRoleParams struct {
	Name           string          `validate:"required"`
	PrivilegesJson class.ArrString `validate:"required" default:"[]" comment:"[a,b,c]"`
	Extend         class.MapString
}

func CreateRole(params CreateRoleParams) {
	role := &model.Role{}
	role.Name.Set(params.Name)
	role.Privileges = params.PrivilegesJson
	role.CreateDt.Set(time.Now())
	role.Extend.Set(params.Extend)
	rdao := roledao.New(roledao.OptsDefault)
	rdao.InsertObj(role)
}

type UpdateRoleParams struct {
	Id             int64 `validate:"required"`
	Name           class.String
	PrivilegesJson class.ArrString `comment:"数组json字符串：[a,b,c]"`
	Extend         class.MapString
}

func UpdateRole(params UpdateRoleParams) {
	dao := roledao.New(roledao.OptsDefault)
	role := dao.SelectOneById(params.Id)
	if role == nil {
		panic(exception.New("角色不存在"))
	}
	if params.Name.Valid {
		role.Name.Set(params.Name.String)
	}
	if params.PrivilegesJson.Valid {
		role.Privileges = params.PrivilegesJson
	}
	if params.Extend.IsValid() {
		role.Extend.PutAll(params.Extend.Map)
	}
	role.UpdateDt.Set(time.Now())
	dao.UpdateObj(role)
	// 权限码变了，该角色下所有用户的鉴权结果都要立即跟着变
	InvalidateRolePrincipals(role.Id)
}

type DeleteRoleParams struct {
	Id int64 `validate:"required"`
}

// DeleteRole 角色占用检查经 sys_user_role 中间表计数，避免拉取全量用户列表。
func DeleteRole(id int64) {
	dao := roledao.New(roledao.OptsNone)
	role := dao.SelectOneById(id)
	if role == nil {
		panic(exception.New("角色不存在"))
	}
	if role.Immutable.Bool {
		panic(exception.New("该角色不可删除"))
	}
	if userroledao.New(userroledao.OptsNone).CountByRoleId(id) > 0 {
		panic(exception.New("角色下还有用户,不能删除"))
	}
	dao.DeleteById(role.Id)
}

// ListRoles B14: 用 model.EnsurePrivileges 替代 controller 中的循环修补。
func ListRoles() []*model.Role {
	dao := roledao.New(roledao.OptsDefault)
	roles := dao.List()
	for _, r := range roles {
		r.EnsurePrivileges() // B14
	}
	return roles
}

type ListRolesWithUserParams struct {
	RoleId int64 `validate:"required"`
}

// ListRolesWithUser B12: 修复 bug — 原代码循环中每个 role 都查 params.RoleId 的用户，
// 应改为查当前 r.Id 的用户。
func ListRolesWithUser(params ListRolesWithUserParams) []*model.Role {
	dao := roledao.New(roledao.OptsDefault)
	list := dao.List()
	udao := userdao.New(userdao.OptsDefault)
	for _, r := range list {
		r.EnsurePrivileges()
		r.Extend.PutAll(map[string]any{
			"users": udao.List(userdao.ListParam{Roles: []int64{r.Id}}), // B12: r.Id 而非 params.RoleId
		})
	}
	return list
}
