package model

import (
	"github.com/example/go-frame/pkg/class"
)

// UserRole 用户-角色关联表（多对多中间表），组合主键 (userid, roleid)，无逻辑删除。
// 角色绑定为整体替换语义：先删该 userid 全部行，再批量插入新绑定。
type UserRole struct {
	UserId   int64      `json:"userId" db:"userid" pk:"true" table:"sys_user_role"`
	RoleId   int64      `json:"roleId" db:"roleid" pk:"true"`
	CreateDt class.Time `json:"createDt,omitempty" db:"createdt"`
}
