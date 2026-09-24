package roledao

import (
	"github.com/example/go-frame/mod/user/model"
	"github.com/example/go-frame/pkg/service/sqlkit"
)

type Dao struct {
	sqlkit.Dao[model.Role]
}

// CascadeOpts 统一级联策略签名。Role 为全局对象、无任何级联关系，opts 仅用于签名一致性。
type CascadeOpts struct{}

var (
	OptsNone    = CascadeOpts{}
	OptsDefault = CascadeOpts{}
)

// New 按 CascadeOpts 构造 dao。与其它 dao 统一签名，opts 当前不使用。
func New(opts CascadeOpts, ds ...*sqlkit.DataSource) Dao {
	return Dao{sqlkit.New[model.Role](ds...)}
}

func (dao Dao) FindByName(name string) *model.Role {
	// S4: One() 内部已默认 LIMIT 1
	return dao.Select().Where("name=?", name).One()
}

// List 列出全部普通角色（id>0 排除 id=0 的内置超级管理员角色）。
func (dao Dao) List() []*model.Role {
	return dao.Select().Where("id>0").OrderBy("id").List()
}
