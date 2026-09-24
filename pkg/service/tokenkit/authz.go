package tokenkit

import (
	"strings"
	"time"

	"github.com/example/go-frame/pkg/library/jsonkit"
	"github.com/example/go-frame/pkg/service/cachekit"
)

// 本文件提供「当前登录用户能做什么」的判定能力，与 token.go 的会话管理同属一个包：
// 鉴权链路既要读会话又要读权限主体，拆成两个包会逼出交叉依赖。
//
// 权限数据源由业务模块注入（tokenkit 不依赖 mod/*，符合 pkg 不得反向依赖业务模块的约定）。
// mod/user 在 Init 中调用 SetLoader 注入 userdao 的实现。

// principalCacheTtl 权限主体本地缓存时长。
// 权限判定每次都查库会给鉴权链路加一条 SQL；这里缓存一小段时间换取性能，
// 代价是改角色后最长有 1 分钟的生效延迟。改角色后应主动调用 InvalidatePrincipal。
const principalCacheTtl = time.Minute

// principalCacheKey 权限主体缓存 key。userId 为不透明字符串，直接作 key 后缀。
func principalCacheKey(userId string) string { return "authz:principal:" + userId }

// Principal 当前登录用户的权限主体。
// 与 model.User 的区别：这里只保留鉴权需要的扁平字段，且由 Loader 从业务模型转换而来。
type Principal struct {
	// UserId 用户标识，与 Session.UserId 同为不透明字符串。
	UserId string `json:"userId"`
	// Department 用户所属部门 id，0 表示无部门。
	Department int64 `json:"department"`
	// Roles 用户持有的角色 id 列表。
	Roles []int64 `json:"roles"`
	// Privileges 所有角色的权限码并集。
	Privileges []string `json:"privileges"`
	// Exclude 显式剔除的权限码（对应 sys_user.extend.privilegeExclude），
	// 优先级高于 Privileges——用于给个别用户开「除某项外全部权限」。
	Exclude []string `json:"exclude"`
}

// Loader 按用户 id 装配权限主体，由业务模块实现。
// 用函数类型而非单方法接口：业务侧实现通常就是一个包级函数，
// 接口会平白要求一个无意义的方法名包装。
type Loader func(userId string) *Principal

var loader Loader

// SetLoader 注册权限数据源。应在服务启动、路由挂载之前调用（如 mod/user.Init）。
// 重复调用以最后一次为准。
func SetLoader(l Loader) { loader = l }

// HasLoader 是否已注册权限数据源。未注册时所有权限判定一律拒绝（fail closed）。
func HasLoader() bool { return loader != nil }

// PrincipalOf 装配并返回用户权限主体，带一分钟本地缓存。
//
// 未注册 Loader 或用户不存在时返回 nil——调用方必须按「无权限」处理，
// 绝不能把 nil 当作「不限制」，否则未装配权限数据源的部署会全量放行。
func PrincipalOf(userId string) *Principal {
	if userId == "" || loader == nil {
		return nil
	}
	key := principalCacheKey(userId)
	if cached := cachekit.Get(key); cached != "" {
		p := &Principal{}
		if err := jsonkit.ParseObj(cached, p); err == nil {
			return p
		}
		// 缓存内容损坏，丢弃后回源
		cachekit.Del(key)
	}
	p := loader(userId)
	if p == nil {
		return nil
	}
	// Loader 可以不填 UserId（如直接用 dao 返回的实体转换），以入参为准补齐
	if p.UserId == "" {
		p.UserId = userId
	}
	cachekit.Set(key, jsonkit.ToString(p), &cachekit.Param{Ttl: principalCacheTtl})
	return p
}

// InvalidatePrincipal 清空某用户的权限主体缓存。
// 修改用户角色、部门、权限剔除项后必须调用，否则变更最长延迟一分钟才生效。
func InvalidatePrincipal(userId string) {
	cachekit.Del(principalCacheKey(userId))
}

// IsSuperAdmin 是否持有内置超级管理员角色（model.RoleIdSuperAdmin）。
//
// ⚠️ 该角色在本项目中是「禁止被管理员接口改绑/删除」的受保护内置角色，
// 并非隐式拥有全部权限的超级账号——是否放行一切由其 privileges 决定（种子数据为 ["*"]）。
func (p *Principal) IsSuperAdmin(superAdminRoleId int64) bool {
	return p != nil && p.HasRole(superAdminRoleId)
}

// HasRole 是否持有给定角色之一。
func (p *Principal) HasRole(roleIds ...int64) bool {
	if p == nil || len(p.Roles) == 0 {
		return false
	}
	for _, want := range roleIds {
		for _, r := range p.Roles {
			if r == want {
				return true
			}
		}
	}
	return false
}

// HasPerm 是否拥有给定权限之一（OR 语义）。
// 支持通配：持有 "user:*" 即通过 "user:add"；持有 "*" 即通过一切。
func (p *Principal) HasPerm(perms ...string) bool {
	if p == nil {
		return false
	}
	for _, want := range perms {
		if p.HasPrivilege(want) {
			return true
		}
	}
	return false
}

// HasPrivilege 单个权限判定：任一角色的权限集合命中，且未被 exclude 剔除。
// 语义与 model.User.HasPrivilege 一致，额外支持 "*" 通配。
func (p *Principal) HasPrivilege(perm string) bool {
	if p == nil || perm == "" {
		return false
	}
	for _, ex := range p.Exclude {
		if ex == perm || ex == "*" {
			return false
		}
	}
	for _, granted := range p.Privileges {
		if matchPrivilege(granted, perm) {
			return true
		}
	}
	return false
}

// HasDept 是否属于给定部门之一（OR 语义），用户无部门时恒为 false。
func (p *Principal) HasDept(deptIds ...int64) bool {
	if p == nil || p.Department == 0 {
		return false
	}
	for _, want := range deptIds {
		if p.Department == want {
			return true
		}
	}
	return false
}

// IsDeptUnder 判断用户部门是否为 deptId 本身或其子孙部门。
//
// ⚠️ 依赖调用方传入的 descendants 已展开完整子树——本方法不查库，
// 子树展开由 mod/user 的 service 层用递归 CTE 完成（见 service.DepartmentsOf）。
func (p *Principal) IsDeptUnder(deptId int64, descendants []int64) bool {
	if p == nil || p.Department == 0 {
		return false
	}
	if p.Department == deptId {
		return true
	}
	for _, d := range descendants {
		if d == p.Department {
			return true
		}
	}
	return false
}

// matchPrivilege 权限码匹配：
//   - 完全相等；
//   - "user:*" 末段通配，命中 "user:" 前缀下任意深度（user:add / user:admin:list）；
//   - 任意段为 "*" 时该段通配，此时要求总段数相同，避免 "*:add" 意外放行 "a:b:add"。
func matchPrivilege(pattern, target string) bool {
	if pattern == target || pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return false
	}
	ps := strings.Split(pattern, ":")
	ts := strings.Split(target, ":")
	for i, seg := range ps {
		if seg == "*" {
			if i == len(ps)-1 {
				// 末段通配：吞掉剩余所有层级
				return true
			}
			continue
		}
		if i >= len(ts) || ts[i] != seg {
			return false
		}
	}
	return len(ps) == len(ts)
}
