package model

import (
	"github.com/example/go-frame/pkg/class"
)

type PrivilegeConstant struct {
	Id   string       `json:"id" db:"id" pk:"true" table:"sys_privilege_constant"`
	Name class.String `json:"name,omitempty" db:"name"`
	Type class.String `json:"type,omitempty" db:"type" comment:"暂不用"`
	Sort int32        `json:"sort" db:"sort"`
}

// 接口级权限码。约定 "<模块>:<操作>"，由 sys_role.privileges 承载、
// middleware.AuthPerm 消费；支持通配（"user:*" 覆盖 user 下全部权限，"*" 覆盖一切）。
//
// 与 sql/user.*.sql 中 sys_privilege_constant 的种子数据一一对应，
// 新增权限码时两边同步补充，否则字典接口不会展示但鉴权仍会生效（易造成排查困难）。
const (
	// PermUserAdmin 用户管理：增删改查用户、分配角色与部门。
	PermUserAdmin = "user:admin"
	// PermUserSession 在线会话管理：查看在线用户、强制下线。
	PermUserSession = "user:session"
	// PermRoleManage 角色与权限码管理。
	PermRoleManage = "role:manage"
	// PermDeptManage 部门管理。
	PermDeptManage = "department:manage"
)
