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
