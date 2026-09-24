package sqlkit

// ============ S13: CascadeLink 声明式批量级联 ============
// 把"收集 id → 批量查询 → 按 id 分发"的标准模式抽成可复用的 link，
// dao 只需声明 T→*U 的字段映射与加载函数，不再重复写收集/去重/分发逻辑。
//
// 适用模式：T 持有 *U 字段，U 通过主键 Id 标识，需要按 U.Id 批量加载 U 后回填到 T。
// 典型场景：User.Role、User.Department、Role.Department、Department.Parent。
//
// 不适用模式（需用 WithCascadeBatchOpts 自定义）：
//   - 反向查询（如 Department.Children：通过 T.Id 反查 U.parent IN (...)）
//   - 字段重置语义（如 department 的 !opts.Parent 时强制 Parent=nil）
//   - 自定义过滤/排序
//
// 一对多（经中间表，如 User.Roles ← sys_user_role → Role）见 NewCascadeManyLink。

// CascadeLinker 单条级联关系的抽象接口。
// T 为外层实体类型（如 User），描述 T 上一个 *U 字段的批量加载策略。
type CascadeLinker[T any] interface {
	applyBatch(list []*T, ds *DataSource)
}

// cascadeLink T→*U 单对象级联的标准实现。
type cascadeLink[T any, U any] struct {
	get  func(*T) *U                            // 从 T 读取当前 U（含 Id），nil 表示该 T 无此级联
	set  func(*T, *U)                           // 把加载到的 U 写回 T
	idOf func(*U) int64                         // 提取 U 的主键 id
	load func(ids []int64, ds *DataSource) []*U // 按 id 列表批量加载 U（通常调用目标 dao 的 SelectByIdsIgnoreDel）
}

func (l cascadeLink[T, U]) applyBatch(list []*T, ds *DataSource) {
	// 收集 id（去 nil、去重）
	var ids []int64
	seen := map[int64]struct{}{}
	for _, t := range list {
		if t == nil {
			continue
		}
		u := l.get(t)
		if u == nil {
			continue
		}
		id := l.idOf(u)
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	// 批量加载
	loaded := l.load(ids, ds)
	byId := make(map[int64]*U, len(loaded))
	for _, u := range loaded {
		byId[l.idOf(u)] = u
	}
	// 按 id 分发回原 list
	for _, t := range list {
		if t == nil {
			continue
		}
		u := l.get(t)
		if u == nil {
			continue
		}
		if v, ok := byId[l.idOf(u)]; ok {
			l.set(t, v)
		}
	}
}

// NewCascadeLink 构造一个 T→*U 的级联 link。
//   - get: 从 T 读取当前 U（含 Id），返回 nil 表示该 T 无此级联
//   - set: 把加载到的完整 U 写回 T
//   - idOf: 提取 U 的主键 id
//   - load: 按 id 列表批量加载 U（通常调用目标 dao 的 SelectByIdsIgnoreDel）
//
// 示例：
//
//	sqlkit.NewCascadeLink(
//	    func(u *model.User) *model.Role { return u.Role },
//	    func(u *model.User, r *model.Role) { u.Role = r },
//	    func(r *model.Role) int64 { return r.Id },
//	    func(ids []int64, ds *sqlkit.DataSource) []*model.Role {
//	        return roledao.New(roledao.OptsDefault, ds).SelectByIdsIgnoreDel(ids)
//	    },
//	)
func NewCascadeLink[T any, U any](
	get func(*T) *U,
	set func(*T, *U),
	idOf func(*U) int64,
	load func(ids []int64, ds *DataSource) []*U,
) CascadeLinker[T] {
	return cascadeLink[T, U]{get: get, set: set, idOf: idOf, load: load}
}

// cascadeManyLink T→[]U（经中间表关联）的一对多级联标准实现。
type cascadeManyLink[T any, U any] struct {
	ownerIdsOf func(*T) []int64                                      // 提取 T 的主键（通常单元素切片；组合主键可多元素）
	set        func(*T, []*U)                                        // 回填 []*U
	loadPairs  func(ownerIds []int64, ds *DataSource) map[int64][]*U // 一次批量查询，组装 ownerId → []*U
}

func (l cascadeManyLink[T, U]) applyBatch(list []*T, ds *DataSource) {
	var ids []int64
	seen := map[int64]struct{}{}
	for _, t := range list {
		if t == nil {
			continue
		}
		for _, id := range l.ownerIdsOf(t) {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	byOwner := l.loadPairs(ids, ds)
	for _, t := range list {
		if t == nil {
			continue
		}
		ownerIds := l.ownerIdsOf(t)
		if len(ownerIds) == 0 {
			continue
		}
		if v, ok := byOwner[ownerIds[0]]; ok {
			l.set(t, v)
		}
	}
}

// NewCascadeManyLink 构造 T→[]U（经中间表关联）的一对多级联 link。
//   - ownerIdsOf: 提取 T 的主键 id 列表（单主键返回 []int64{t.Id}）
//   - set: 把该 T 的全部关联 U 写回（如 u.Roles = roles）
//   - loadPairs: 一次批量查询并组装 ownerId → []*U 映射。
//     中间表查询、目标实体加载、去重与排序均由该函数负责（通常两段查询：
//     先查中间表拿关联，再批量查 U 组装），避免 N+1。
//
// 示例（User.Roles via sys_user_role）：
//
//	sqlkit.NewCascadeManyLink(
//	    func(u *model.User) []int64 { return []int64{u.Id} },
//	    func(u *model.User, roles []*model.Role) { u.Roles = roles },
//	    func(ids []int64, ds *sqlkit.DataSource) map[int64][]*model.Role {
//	        return userroledao.New(userroledao.OptsNone, ds).LoadRolesByUserIds(ids)
//	    },
//	)
func NewCascadeManyLink[T any, U any](
	ownerIdsOf func(*T) []int64,
	set func(*T, []*U),
	loadPairs func(ownerIds []int64, ds *DataSource) map[int64][]*U,
) CascadeLinker[T] {
	return cascadeManyLink[T, U]{ownerIdsOf: ownerIdsOf, set: set, loadPairs: loadPairs}
}

// BuildCascadeBatch 把多个 link 组装为批量级联回调。
// 调用方通常不直接用此函数，而是用 Dao.WithCascadeBatchLinks 一次注册。
func BuildCascadeBatch[T any](links ...CascadeLinker[T]) CascadeBatchFunc[T] {
	return func(list []*T, ctx CascadeCtx) {
		if len(list) == 0 {
			return
		}
		for _, l := range links {
			l.applyBatch(list, ctx.Ds)
		}
	}
}

// WithCascadeBatchLinks S13: 用声明式 link 列表同时设置 Cascade 与 CascadeBatch。
// opts 会随 CascadeCtx.Opts 传入；单条路径（One）走 list 长度1 的批量调用，避免重复实现。
//
// 适用于纯"T 持有 *U"模式的级联。
// 含反向查询（如 Department.Children）或字段重置等特殊语义时，请用 WithCascadeBatchOpts 自定义。
func (dao Dao[T]) WithCascadeBatchLinks(opts any, links ...CascadeLinker[T]) Dao[T] {
	batchF := BuildCascadeBatch(links...)
	dao.CascadeBatch = func(list []*T) {
		batchF(list, CascadeCtx{Opts: opts, Ds: dao.dataSource})
	}
	dao.Cascade = func(obj *T) {
		batchF([]*T{obj}, CascadeCtx{Opts: opts, Ds: dao.dataSource})
	}
	return dao
}
