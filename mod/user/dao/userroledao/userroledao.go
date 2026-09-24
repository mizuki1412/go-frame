package userroledao

import (
	"time"

	"github.com/example/go-frame/mod/user/dao/roledao"
	"github.com/example/go-frame/mod/user/model"
	"github.com/example/go-frame/pkg/service/sqlkit"
)

// Dao 用户-角色关联表 sys_user_role。
type Dao struct {
	sqlkit.Dao[model.UserRole]
}

// CascadeOpts 统一级联策略签名。本表无级联关系，opts 仅用于签名一致性。
type CascadeOpts struct{}

var OptsNone = CascadeOpts{}

// New 按 CascadeOpts 构造 dao。与其它 dao 统一签名，opts 当前不使用。
func New(opts CascadeOpts, ds ...*sqlkit.DataSource) Dao {
	return Dao{sqlkit.New[model.UserRole](ds...)}
}

// LoadRolesByUserIds 批量装配 userId → []*Role：先查中间表拿 (userid, roleid)，
// 再一次性批量加载 Role 后按 userid 分组，整批仅 2 条 SQL，供 userdao 的
// Roles 级联（NewCascadeManyLink）使用。
func (dao Dao) LoadRolesByUserIds(userIds []int64) map[int64][]*model.Role {
	byOwner := make(map[int64][]*model.Role)
	if len(userIds) == 0 {
		return byOwner
	}
	links := dao.Select().WhereUnnestIn("userid", userIds).OrderBy("roleid").List()
	if len(links) == 0 {
		return byOwner
	}
	var roleIds []int64
	seen := make(map[int64]struct{}, len(links))
	for _, l := range links {
		if _, ok := seen[l.RoleId]; !ok {
			seen[l.RoleId] = struct{}{}
			roleIds = append(roleIds, l.RoleId)
		}
	}
	roles := roledao.New(roledao.OptsNone, dao.DataSource()).SelectByIdsIgnoreDel(roleIds)
	roleById := make(map[int64]*model.Role, len(roles))
	for _, r := range roles {
		roleById[r.Id] = r
	}
	for _, l := range links {
		if r, ok := roleById[l.RoleId]; ok {
			byOwner[l.UserId] = append(byOwner[l.UserId], r)
		}
	}
	return byOwner
}

// ListUserIdsByRoleIds 反查持有任一角色的用户 id（去重）。
func (dao Dao) ListUserIdsByRoleIds(roleIds []int64) []int64 {
	if len(roleIds) == 0 {
		return nil
	}
	rows := dao.Select().WhereUnnestIn("roleid", roleIds).
		Columns("userid").Distinct().List()
	userIds := make([]int64, 0, len(rows))
	for _, r := range rows {
		userIds = append(userIds, r.UserId)
	}
	return userIds
}

// CountByRoleId 统计某角色下的用户数，用于角色删除前的占用检查。
func (dao Dao) CountByRoleId(roleId int64) int64 {
	return dao.Select().Where("roleid=?", roleId).Count()
}

// ReplaceForUser 整体替换用户角色绑定：删除该用户全部旧绑定后批量插入新绑定。
// 调用方负责事务（sqlkit.TxArea）。roleIds 去重，避免 (userid, roleid) 主键冲突。
func (dao Dao) ReplaceForUser(userId int64, roleIds []int64) {
	dao.Delete().Where("userid=?", userId).Exec()
	if len(roleIds) == 0 {
		return
	}
	now := time.Now()
	seen := make(map[int64]struct{}, len(roleIds))
	list := make([]*model.UserRole, 0, len(roleIds))
	for _, rid := range roleIds {
		if _, ok := seen[rid]; ok {
			continue
		}
		seen[rid] = struct{}{}
		ur := &model.UserRole{UserId: userId, RoleId: rid}
		ur.CreateDt.Set(now)
		list = append(list, ur)
	}
	dao.InsertBatch(list)
}

// DeleteByUserId 删除用户的全部角色绑定（用户删除时调用）。
func (dao Dao) DeleteByUserId(userId int64) int64 {
	return dao.Delete().Where("userid=?", userId).Exec()
}
