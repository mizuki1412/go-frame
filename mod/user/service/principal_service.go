package service

import (
	"strconv"

	"github.com/example/go-frame/mod/user/dao/departmentdao"
	"github.com/example/go-frame/mod/user/dao/userdao"
	"github.com/example/go-frame/mod/user/dao/userroledao"
	"github.com/example/go-frame/pkg/service/tokenkit"
	"github.com/spf13/cast"
)

// 本文件是 mod/user 与 tokenkit 之间的桥：tokenkit 只管会话与鉴权，不认识业务模型，
// 也不对用户 id 的类型做任何假设。mod/user 的 sys_user.id 是 bigint，
// 转换**只发生在本包边界**（uidOf / cast.ToInt64），包外一律传字符串。

// uidOf 把本模块的数值型用户 id 转成 tokenkit 使用的不透明字符串。
func uidOf(id int64) string { return strconv.FormatInt(id, 10) }

// LoadPrincipal 实现 tokenkit.Loader：按用户 id 装配权限主体。
//
// 入参是 tokenkit 传来的字符串 id（登录时由 uidOf 写入），这里转回 bigint 查库。
// 非数字串说明上游写入了非法 id，直接按「用户不存在」处理——绝不放行。
//
// 一次查询同时带出 Roles（含每个角色的 privileges 权限码数组）与 Department，
// 即每个用户一条 SELECT + 一条中间表批量查询，无 N+1。
//
// 未注册 Loader 时 tokenkit 侧一律拒绝（fail closed），不会退化成「不校验」。
func LoadPrincipal(userId string) *tokenkit.Principal {
	dbId := cast.ToInt64(userId)
	if dbId == 0 {
		return nil
	}
	user := userdao.New(userdao.OptsDefault).SelectOneById(dbId)
	if user == nil {
		return nil
	}
	p := &tokenkit.Principal{
		UserId:     userId,
		Roles:      make([]int64, 0, len(user.Roles)),
		Privileges: make([]string, 0),
		Exclude:    user.Extend.GetArrString("privilegeExclude"),
	}
	if user.Department != nil {
		p.Department = user.Department.Id
	}
	for _, r := range user.Roles {
		if r == nil {
			continue
		}
		p.Roles = append(p.Roles, r.Id)
		p.Privileges = append(p.Privileges, r.Privileges.Array...)
	}
	return p
}

// InvalidateRolePrincipals 角色定义（权限码）变更后，清空该角色下所有用户的权限主体缓存。
//
// tokenkit 的权限主体有 1 分钟本地缓存（避免每次鉴权都打一条 SQL），
// 代价是改角色后不主动失效就需要等缓存自然过期。角色变更影响面是「该角色下的用户」，
// 经中间表反查用户 id 一次即可，无需全表清缓存。
func InvalidateRolePrincipals(roleId int64) {
	ids := userroledao.New(userroledao.OptsNone).ListUserIdsByRoleIds([]int64{roleId})
	for _, uid := range ids {
		tokenkit.InvalidatePrincipal(uidOf(uid))
	}
}

// DepartmentsOf 返回用户所属部门的数据范围 id 列表。
//
//   - includeSelf=false：只返回本部门
//   - includeSelf=true ：返回本部门及其所有子孙部门（「本部门及以下」）
//
// 纯 DB 语义，与 tokenkit 无关，故入参仍是 int64。
// 用户无部门时返回空列表，调用方须按「查不到任何数据」处理而非放行全表——
// 部门是唯一的数据范围依据，放行等于越权。
func DepartmentsOf(userId int64, includeSelf bool) []int64 {
	dao := userdao.New(userdao.OptsDeptOnly)
	user := dao.SelectOneById(userId)
	if user == nil || user.Department == nil {
		return nil
	}
	if !includeSelf {
		return []int64{user.Department.Id}
	}
	return departmentdao.New(departmentdao.OptsNone).ListSubtreeIds(user.Department.Id)
}
