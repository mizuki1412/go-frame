package userdao

import (
	"fmt"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/example/go-frame/mod/user/dao/departmentdao"
	"github.com/example/go-frame/mod/user/dao/userroledao"
	"github.com/example/go-frame/mod/user/model"
	"github.com/example/go-frame/pkg/library/stringkit"
	"github.com/example/go-frame/pkg/service/sqlkit"
)

type Dao struct {
	sqlkit.Dao[model.User]
}

// CascadeOpts S10/S11: 级联策略选项，替代 byte 枚举。
type CascadeOpts struct {
	// Roles 是否级联取 Roles（多角色，经 sys_user_role 中间表）
	Roles bool
	// Department 是否级联取 Department
	Department bool
}

// 预置常用策略
var (
	OptsNone      = CascadeOpts{}                              // 不级联
	OptsDefault   = CascadeOpts{Roles: true, Department: true} // 默认全级联
	OptsRolesOnly = CascadeOpts{Roles: true}                   // 仅 Roles
	OptsDeptOnly  = CascadeOpts{Department: true}              // 仅 Department
)

// New 按 CascadeOpts 构造 dao。
// S13: 用 WithCascadeBatchLinks + 声明式 link 替代手写收集/分发逻辑。
// Roles 用一对多 link（NewCascadeManyLink）：经 sys_user_role 中间表批量装配，
// 整批 2 条 SQL，无 N+1。Department 用单对象 link。
func New(opts CascadeOpts, ds ...*sqlkit.DataSource) Dao {
	dao := sqlkit.New[model.User](ds...)
	var links []sqlkit.CascadeLinker[model.User]
	if opts.Roles {
		links = append(links, sqlkit.NewCascadeManyLink(
			func(u *model.User) []int64 { return []int64{u.Id} },
			func(u *model.User, roles []*model.Role) { u.Roles = roles },
			func(ids []int64, ds *sqlkit.DataSource) map[int64][]*model.Role {
				return userroledao.New(userroledao.OptsNone, ds).LoadRolesByUserIds(ids)
			},
		))
	}
	if opts.Department {
		links = append(links, sqlkit.NewCascadeLink(
			func(u *model.User) *model.Department { return u.Department },
			func(u *model.User, d *model.Department) { u.Department = d },
			func(d *model.Department) int64 { return d.Id },
			func(ids []int64, ds *sqlkit.DataSource) []*model.Department {
				return departmentdao.New(departmentdao.OptsDefault, ds).SelectByIdsIgnoreDel(ids)
			},
		))
	}
	return Dao{dao.WithCascadeBatchLinks(opts, links...)}
}

// Login 按用户名/手机号+密码（等值）查询用户。
//
// Deprecated: 密码已迁移 bcrypt——服务层 Login 改为"先取回用户、Go 内校验、
// 惰性升级"（cryptokit.CheckPwd/HashPwd/NeedUpgrade）。bcrypt 哈希带随机盐，
// 同一密码每次结果不同，不适合 DB 等值匹配（pwd=? 永远查不中），此方法仅作
// 兼容保留，请勿新增调用。
func (dao Dao) Login(pwd, username, phone string) *model.User {
	builder := dao.Select()
	if !stringkit.IsNull(username) {
		builder = builder.Where("username=?", username)
	} else {
		builder = builder.Where("phone=?", phone)
	}
	// S4: One() 内部已默认 LIMIT 1，无需再追加 Limit(1)
	return builder.Where("pwd=?", pwd).One()
}

func (dao Dao) FindByPhone(phone string) *model.User {
	return dao.Select().Where("phone=?", phone).One()
}

func (dao Dao) FindByUsername(username string) *model.User {
	return dao.Select().Where("username=?", username).One()
}

// FindByUsernameDeleted S1: 用 OneIgnoreDel 真正忽略逻辑删除过滤。
func (dao Dao) FindByUsernameDeleted(username string) *model.User {
	return dao.Select().Where("username=?", username).OneIgnoreDel()
}

// FindParam 可以通过extend的值来find
type FindParam struct {
	Extend map[string]any
}

// Find S3: 用 WhereJsonbPathEq 替代 fmt.Sprintf 拼接，消除 SQL 注入风险。
func (dao Dao) Find(param FindParam) *model.User {
	builder := dao.Select()
	for k, v := range param.Extend {
		builder = builder.WhereJsonbPathEq("extend", k, v)
	}
	return builder.One()
}

// ListFromRootDepart S2: 用 WithRecursiveRaw 替代 fmt.Sprintf 拼接递归 CTE。
// B22: 参数化 rootId，去掉 PG 专用的 ::bigint cast，兼容多 driver。
func (dao Dao) ListFromRootDepart(departId int64) []*model.User {
	cteBody := fmt.Sprintf(`select ? as id union all select d.id from %s d, t where t.id=d.parent`,
		departmentdao.New(departmentdao.OptsNone, dao.DataSource()).Table())
	return dao.Select().
		WithRecursiveRaw("t", []string{"id"}, cteBody, departId).
		Where("department in (select id from t)").
		OrderBy("name").OrderBy("id").List()
}

func (dao Dao) CountFromRootDepart(departId int64) int64 {
	cteBody := fmt.Sprintf(`select ? as id union all select d.id from %s d, t where t.id=d.parent`,
		departmentdao.New(departmentdao.OptsNone, dao.DataSource()).Table())
	return dao.Select().
		WithRecursiveRaw("t", []string{"id"}, cteBody, departId).
		Where("department in (select id from t)").
		Count()
}

type ListParam struct {
	// Roles 持有任一角色的用户（经 sys_user_role 反查）
	Roles []int64
	// Departments 直属部门的用户
	Departments []int64
	IdList      []int64
	// B21: 分页参数，PageSize=0 时不分页
	Page *sqlkit.Page
}

func (dao Dao) List(param ListParam) model.UserList {
	// 角色过滤先经中间表反查 user id，再走主表 id IN 过滤（两段 SQL，无 join、无 N+1）。
	// 反查结果为空时直接短路，避免 IN () 空集合产生全表扫描的语义歧义。
	var roleUserIds []int64
	if len(param.Roles) > 0 {
		roleUserIds = userroledao.New(userroledao.OptsNone, dao.DataSource()).ListUserIdsByRoleIds(param.Roles)
		if len(roleUserIds) == 0 {
			return model.UserList{}
		}
	}
	builder := dao.Select().OrderBy("name").OrderBy("id")
	if len(roleUserIds) > 0 {
		builder = builder.WhereUnnestIn("id", roleUserIds)
	}
	if len(param.IdList) > 0 {
		builder = builder.WhereUnnestIn("id", param.IdList)
	}
	if len(param.Departments) > 0 {
		builder = builder.WhereUnnestIn("department", param.Departments)
	}
	if param.Page != nil && param.Page.PageSize > 0 {
		list, _ := builder.Page(*param.Page)
		return list
	}
	return builder.List()
}

// FreezeUser 冻结/解冻用户，顺带刷新 updatedt。
func (dao Dao) FreezeUser(uid int64, status int32) {
	dao.Update().Set("status", status).Set("updatedt", time.Now()).Where("id=?", uid).Exec()
}

// SetNull 删除用户时置空唯一字段（username/phone 为 NULL 规避唯一约束），顺带刷新 updatedt。
func (dao Dao) SetNull(id int64) {
	dao.Update().Set("phone", squirrel.Expr("null")).Set("username", squirrel.Expr("null")).
		Set("updatedt", time.Now()).Where("id=?", id).Exec()
}
