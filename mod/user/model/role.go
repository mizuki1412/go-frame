package model

import (
	"database/sql/driver"

	"github.com/example/go-frame/pkg/class"
	"github.com/spf13/cast"
)

// Role 角色表。角色为全局对象，不绑定部门——数据范围只由用户的 Department 决定。
type Role struct {
	Id          int64           `json:"id" db:"id" pk:"true" table:"sys_role" auto:"true"`
	Name        class.String    `json:"name,omitempty" db:"name"`
	Description class.String    `json:"description,omitempty" db:"description"`
	Privileges  class.ArrString `json:"privileges,omitempty" db:"privileges"`
	Immutable   class.Bool      `json:"immutable,omitempty" db:"immutable" comment:"内置角色，不可删除"`
	Extend      class.MapString `json:"extend,omitempty" db:"extend"`
	CreateDt    class.Time      `json:"createDt,omitempty" db:"createdt"`
	UpdateDt    class.Time      `json:"updateDt,omitempty" db:"updatedt"`
	Deleted     class.Bool      `json:"-" db:"deleted" logicDel:"true"`
}

func (th *Role) Scan(value any) error {
	if value == nil {
		return nil
	}
	id := cast.ToInt64(value)
	th.Id = id
	return nil
}
func (th *Role) Value() (driver.Value, error) {
	return th.Id, nil
}

// EnsurePrivileges B14: 保证 Privileges 字段为有效空数组，避免 JSON 序列化出 null。
func (th *Role) EnsurePrivileges() {
	if th == nil {
		return
	}
	if !th.Privileges.Valid {
		th.Privileges.Valid = true
		th.Privileges.Array = []string{}
	}
}

type RoleList []*Role

func (l RoleList) Len() int           { return len(l) }
func (l RoleList) Swap(i, j int)      { l[i], l[j] = l[j], l[i] }
func (l RoleList) Less(i, j int) bool { return l[i].Id < l[j].Id }
func (l RoleList) Filter(fun func(ele *Role) bool) RoleList {
	arr := make(RoleList, 0, len(l))
	for _, e := range l {
		if fun(e) {
			arr = append(arr, e)
		}
	}
	return arr
}
func (l RoleList) Find(fun func(ele *Role) bool) *Role {
	for _, e := range l {
		if fun(e) {
			return e
		}
	}
	return nil
}
func (l RoleList) Map(fun func(ele *Role) any) []any {
	var results []any
	for _, e := range l {
		results = append(results, fun(e))
	}
	return results
}
