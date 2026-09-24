package departmentdao

import (
	"fmt"

	"github.com/example/go-frame/mod/user/model"
	"github.com/example/go-frame/pkg/service/sqlkit"
)

type Dao struct {
	sqlkit.Dao[model.Department]
}

// CascadeOpts S10/S11: 级联策略选项，替代 byte 枚举。
type CascadeOpts struct {
	// Children 是否级联取子部门
	Children bool
	// Parent 是否级联取父部门
	Parent bool
}

var (
	OptsNone     = CascadeOpts{}                             // 不级联
	OptsDefault  = CascadeOpts{Parent: true}                 // 默认级联父部门
	OptsChildren = CascadeOpts{Children: true}               // 仅子部门
	OptsAll      = CascadeOpts{Children: true, Parent: true} // 子+父
)

// New 按 CascadeOpts 构造 dao。
// S13: Parent 用声明式 link（递归 OptsDefault）；Children 为反向查询，保留 inline。
// B23: 内层查询 children 时用 sqlkit.New 直接查询，不经过 departmentdao.New，
// 避免 OptsNone 的重置逻辑把 c.Parent 设 nil 导致 c.Parent.Id panic。
func New(opts CascadeOpts, ds ...*sqlkit.DataSource) Dao {
	d := sqlkit.New[model.Department](ds...)
	dao := Dao{d}
	// Parent 用声明式 link
	var parentLinks []sqlkit.CascadeLinker[model.Department]
	if opts.Parent {
		parentLinks = append(parentLinks, sqlkit.NewCascadeLink(
			func(dept *model.Department) *model.Department { return dept.Parent },
			func(dept *model.Department, p *model.Department) { dept.Parent = p },
			func(p *model.Department) int64 { return p.Id },
			func(ids []int64, ds *sqlkit.DataSource) []*model.Department {
				// 递归加载父部门的父部门
				return New(OptsDefault, ds).SelectByIdsIgnoreDel(ids)
			},
		))
	}
	parentBatch := sqlkit.BuildCascadeBatch(parentLinks...)
	batchF := func(list []*model.Department, ctx sqlkit.CascadeCtx) {
		o := ctx.Opts.(CascadeOpts)
		// 重置未请求字段（保持旧语义）
		for _, d := range list {
			if d == nil {
				continue
			}
			if !o.Children {
				d.Children = nil
			}
			if !o.Parent {
				d.Parent = nil
			}
		}
		// Parent 批量加载（用 link，递归）
		if o.Parent {
			parentBatch(list, ctx)
		}
		// Children 反向批量加载（按 parent IN (...) 一次查询，按 parent id 分组）
		if o.Children {
			var ids []int64
			for _, d := range list {
				if d != nil {
					ids = append(ids, d.Id)
				}
			}
			if len(ids) > 0 {
				// B23: 用 sqlkit.New 直接查询，不经过 departmentdao.New，
				// 避免 OptsNone 重置 c.Parent=nil 导致后续 c.Parent.Id panic。
				// children 只需 Parent.Id 用于分组，不需完整级联。
				allChildren := sqlkit.New[model.Department](ctx.Ds).Select().
					WhereUnnestIn("parent", ids).OrderBy("no").OrderBy("id").List()
				byParent := make(map[int64][]*model.Department)
				for _, c := range allChildren {
					if c.Parent != nil {
						byParent[c.Parent.Id] = append(byParent[c.Parent.Id], c)
					}
				}
				for _, d := range list {
					if d == nil {
						continue
					}
					d.Children = byParent[d.Id]
					if d.Children == nil {
						d.Children = []*model.Department{}
					}
				}
			}
		}
	}
	dao.Cascade = func(obj *model.Department) {
		batchF([]*model.Department{obj}, sqlkit.CascadeCtx{Opts: opts, Ds: dao.DataSource()})
	}
	dao.CascadeBatch = func(list []*model.Department) {
		batchF(list, sqlkit.CascadeCtx{Opts: opts, Ds: dao.DataSource()})
	}
	return dao
}

func (dao Dao) ListByParent(id int64) model.DeptList {
	return dao.Select().Where("parent=?", id).OrderBy("no").OrderBy("id").List()
}

func (dao Dao) ListAll() model.DeptList {
	return dao.Select().Where("id>0").OrderBy("parent").OrderBy("no").OrderBy("id").List()
}

func (dao Dao) FindByName(name string) *model.Department {
	// S4: One() 内部已默认 LIMIT 1
	return dao.Select().Where("name=?", name).One()
}

func (dao Dao) FindByNo(no string) *model.Department {
	// S4: One() 内部已默认 LIMIT 1
	return dao.Select().Where("no=?", no).One()
}

// ListSubtreeIds 返回 rootId 自身及其所有子孙部门的 id（含 rootId）。
//
// 用于把「本部门及以下」的数据范围展开成可下推到 dao 的部门 id 列表：
// 部门树层级不定，递归 CTE 一次查完，避免逐层 ListByParent 造成 N+1。
// B22: rootId 参数化，不做字符串拼接。
func (dao Dao) ListSubtreeIds(rootId int64) []int64 {
	cteBody := fmt.Sprintf(`select ? as id union all select d.id from %s d, t where t.id=d.parent`, dao.Table())
	list := dao.Select().Columns("id").
		WithRecursiveRaw("t", []string{"id"}, cteBody, rootId).
		Where("id in (select id from t)").
		OrderBy("id").List()
	ids := make([]int64, 0, len(list))
	for _, d := range list {
		ids = append(ids, d.Id)
	}
	return ids
}

// IsDescendant B15: 检查 descendantId 是否是 ancestorId 的后代（含自身）。
// 用于修改 parent 时防止成环：若 newParentId 是 deptId 的后代，则不能设为 parent。
func (dao Dao) IsDescendant(ancestorId, descendantId int64) bool {
	if ancestorId == descendantId {
		return true
	}
	table := dao.Table()
	cteBody := fmt.Sprintf(`select ? as id union all select d.id from %s d, t where t.id=d.parent`, table)
	return dao.Select().
		WithRecursiveRaw("t", []string{"id"}, cteBody, ancestorId).
		Where("id=? and id in (select id from t)", descendantId).
		Count() > 0
}
