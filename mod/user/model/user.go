package model

import (
	"database/sql/driver"

	"github.com/example/go-frame/pkg/class"
	"github.com/spf13/cast"
)

// User 用户表。
// Roles 为多对多关联（中间表 sys_user_role），仅在 dao 级联装配，不落本表列；
// Roles 不带 db tag，sqlkit 不会把它当数据库字段处理。
type User struct {
	Id         int64           `json:"id,omitempty" db:"id" pk:"true" table:"sys_user" auto:"true"`
	Roles      []*Role         `json:"roles,omitempty"`
	Department *Department     `json:"department,omitempty" db:"department"`
	Username   class.String    `json:"username,omitempty" db:"username"`
	Name       class.String    `json:"name,omitempty" db:"name"`
	Phone      class.String    `json:"phone,omitempty" db:"phone"`
	Pwd        class.String    `json:"-" db:"pwd"`
	Gender     class.Int32     `json:"gender,omitempty" db:"gender" comment:"1-男,2-女"`
	Image      class.String    `json:"image,omitempty" db:"image" comment:"头像"`
	Address    class.String    `json:"address,omitempty" db:"address"`
	Status     class.Int32     `json:"status,omitempty" db:"status" comment:"冻结 1"`
	Immutable  class.Bool      `json:"immutable,omitempty" db:"immutable" comment:"内置用户，不可删除/改角色"`
	Deleted    class.Bool      `json:"-" db:"deleted" logicDel:"true"`
	Extend     class.MapString `json:"extend,omitempty" db:"extend" comment:"权限剔除privilegeExclude:[]"`
	CreateDt   class.Time      `json:"createDt,omitempty" db:"createdt"`
	UpdateDt   class.Time      `json:"updateDt,omitempty" db:"updatedt"`
}

const UserStatusOK = 0
const UserStatusFreeze = 1

// RoleIdSuperAdmin 内置超级管理员角色 id：持有该角色的用户不允许被管理员接口修改或删除。
const RoleIdSuperAdmin = 0

func (th *User) Scan(value any) error {
	if value == nil {
		return nil
	}
	id := cast.ToInt64(value)
	th.Id = id
	return nil
}

func (th *User) Value() (driver.Value, error) {
	return th.Id, nil
}

// BelongDepartment 判断用户是否属于某个部门（以 User.Department 为准）
func (th *User) BelongDepartment(department int64) bool {
	return th != nil && th.Department != nil && th.Department.Id == department
}

// HasRole 判断用户是否持有指定角色。
func (th *User) HasRole(roleId int64) bool {
	if th == nil {
		return false
	}
	for _, r := range th.Roles {
		if r != nil && r.Id == roleId {
			return true
		}
	}
	return false
}

// HasPrivilege 判断用户是否拥有某权限：任一角色的权限集合命中，且未被 Extend.privilegeExclude 剔除。
func (th *User) HasPrivilege(privilege string) bool {
	if th == nil {
		return false
	}
	for _, excluded := range th.Extend.GetArrString("privilegeExclude") {
		if excluded == privilege {
			return false
		}
	}
	for _, r := range th.Roles {
		if r != nil && r.Privileges.Contains(privilege) {
			return true
		}
	}
	return false
}

type UserList []*User

func (l UserList) Len() int           { return len(l) }
func (l UserList) Swap(i, j int)      { l[i], l[j] = l[j], l[i] }
func (l UserList) Less(i, j int) bool { return l[i].Id < l[j].Id }
func (l UserList) Filter(fun func(ele *User) bool) UserList {
	arr := make(UserList, 0, len(l))
	for _, e := range l {
		if fun(e) {
			arr = append(arr, e)
		}
	}
	return arr
}
func (l UserList) Find(fun func(ele *User) bool) *User {
	for _, e := range l {
		if fun(e) {
			return e
		}
	}
	return nil
}
func (l UserList) Map(fun func(ele *User) any) []any {
	var results []any
	for _, e := range l {
		results = append(results, fun(e))
	}
	return results
}
