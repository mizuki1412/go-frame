package service

import (
	"github.com/example/go-frame/mod/user/dao/userdao"
	"github.com/example/go-frame/mod/user/model"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/service/tokenkit"
	"github.com/spf13/cast"
)

// 在线会话管理：列表、剔出。会话本体存在 tokenkit，本文件负责补齐用户信息与业务校验。

// OnlineSession 在线会话的对外结构 = tokenkit 会话 + 用户展示信息。
type OnlineSession struct {
	tokenkit.Online
	// UserId 遮蔽内层 tokenkit.Online.UserId 的 string 版，对外暴露为 int64。
	// 放在外层是刻意的：encoding/json 按深度就近取字段，外层同名成员会胜出，
	// 从而让本接口的 userId 与用户模块其余接口（model.User.Id 均为数字）保持一致，
	// 前端不必为这一个列表单独处理字符串 id。
	UserId     int64  `json:"userId"`
	UserName   string `json:"userName"`
	Username   string `json:"username"`
	Avatar     string `json:"avatar"`
	DeptName   string `json:"deptName"`
	SuperAdmin bool   `json:"superAdmin"`
}

// userBrief 在线列表里附带的用户展示信息（仅列表路径需要，避免为单会话加载完整实体）。
type userBrief struct {
	UserName   string
	Username   string
	Avatar     string
	DeptName   string
	SuperAdmin bool
}

// loadBriefs 批量装配用户展示信息，user id 去重后一条 SELECT，无 N+1。
func loadBriefs(userIds []int64) map[int64]userBrief {
	users := userdao.New(userdao.OptsDefault).SelectByIds(userIds)
	briefs := make(map[int64]userBrief, len(users))
	for _, u := range users {
		if u == nil {
			continue
		}
		b := userBrief{
			UserName:   u.Name.String,
			Username:   u.Username.String,
			Avatar:     u.Image.String,
			SuperAdmin: u.HasRole(model.RoleIdSuperAdmin),
		}
		if u.Department != nil {
			b.DeptName = u.Department.Name.String
		}
		briefs[u.Id] = b
	}
	return briefs
}

// ListOnlineSessions 全局在线会话列表，按最近活跃时间倒序分页。
func ListOnlineSessions(offset, limit int) ([]OnlineSession, int64) {
	list, total := tokenkit.ListOnline(offset, limit)
	out := make([]OnlineSession, 0, len(list))
	if len(list) == 0 {
		return out, total
	}
	ids := make([]int64, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for _, o := range list {
		if _, ok := seen[o.UserId]; ok {
			continue
		}
		seen[o.UserId] = struct{}{}
		ids = append(ids, cast.ToInt64(o.UserId))
	}
	briefs := loadBriefs(ids)
	for _, o := range list {
		item := OnlineSession{Online: o, UserId: cast.ToInt64(o.UserId)}
		if b, ok := briefs[item.UserId]; ok {
			item.UserName = b.UserName
			item.Username = b.Username
			item.Avatar = b.Avatar
			item.DeptName = b.DeptName
			item.SuperAdmin = b.SuperAdmin
		}
		out = append(out, item)
	}
	return out, total
}

// ListUserSessions 指定用户的在线会话列表（不附带用户信息——调用方已知道是谁）。
func ListUserSessions(userId int64) []OnlineSession {
	list := tokenkit.ListOnlineOf(uidOf(userId))
	out := make([]OnlineSession, 0, len(list))
	for _, o := range list {
		out = append(out, OnlineSession{Online: o, UserId: userId})
	}
	return out
}

// KickoutUser 剔出用户全部在线会话，返回被踢下线数。
//
// 受保护账号（内置超级管理员）不允许被剔出：一旦被踢，该站点将失去唯一的
// 最高权限入口，只能直接改库恢复。
func KickoutUser(operatorUid, targetUid int64) int {
	if operatorUid == 0 {
		panic(exception.New("登录的用户错误"))
	}
	if operatorUid == targetUid {
		panic(exception.New("不能操作自己"))
	}
	if targetUid == 0 {
		panic(exception.New("用户不存在"))
	}
	target := userdao.New(userdao.OptsRolesOnly).SelectOneById(targetUid)
	if target == nil {
		panic(exception.New("用户不存在"))
	}
	if target.HasRole(model.RoleIdSuperAdmin) {
		panic(exception.New("该用户不能被剔出"))
	}
	return tokenkit.DestroyByUser(uidOf(targetUid))
}

// KickoutToken 强制下线单个会话（管理端按 token 精确踢人）。
func KickoutToken(token string) {
	if token == "" {
		panic(exception.New("token 不能为空"))
	}
	tokenkit.Destroy(token)
}
