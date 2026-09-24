package service

import (
	"time"

	"github.com/example/go-frame/mod/user/dao/departmentdao"
	"github.com/example/go-frame/mod/user/dao/userdao"
	"github.com/example/go-frame/mod/user/model"
	"github.com/example/go-frame/pkg/class"
	"github.com/example/go-frame/pkg/class/exception"
)

type CreateDepartmentParams struct {
	No          class.String `validate:"required"`
	Name        string       `validate:"required"`
	Description class.String
	ParentId    class.Int64
	Extend      class.MapString
}

func CreateDepartment(params CreateDepartmentParams) {
	department := &model.Department{}
	dao := departmentdao.New(departmentdao.OptsNone)
	if params.ParentId.Valid {
		parent := dao.SelectOneById(params.ParentId.Int64)
		if parent == nil {
			panic(exception.New("父级部门不存在"))
		}
		department.Parent = parent
	}
	department.Name.Set(params.Name)
	if params.No.Valid {
		if dao.FindByNo(params.No.String) != nil {
			panic(exception.New("当前编号已被占用"))
		}
		department.No.Set(params.No.String)
	}
	if params.Description.Valid {
		department.Description.Set(params.Description.String)
	}
	department.CreateDt.Set(time.Now())
	department.Extend.Set(params.Extend)
	dao.InsertObj(department)
}

type UpdateDepartmentParams struct {
	Id          int64 `validate:"required"`
	No          class.String
	Name        class.String
	Description class.String
	ParentId    class.Int64
	Extend      class.MapString
}

// UpdateDepartment B15: 修改 parent 时做环检测，防止成环导致递归 CTE 死循环。
func UpdateDepartment(params UpdateDepartmentParams) {
	dao := departmentdao.New(departmentdao.OptsNone)
	department := dao.SelectOneById(params.Id)
	if department == nil {
		panic(exception.New("部门不存在"))
	}
	if params.No.Valid && params.No.String != department.No.String {
		if dao.FindByNo(params.No.String) != nil {
			panic(exception.New("当前编号已被占用"))
		}
		department.No.Set(params.No.String)
	}
	if params.Name.Valid {
		department.Name.Set(params.Name.String)
	}
	if params.Description.Valid {
		department.Description.Set(params.Description.String)
	}
	if params.ParentId.Valid && (department.Parent == nil || params.ParentId.Int64 != department.Parent.Id) {
		// B15: 环检测 — 不能将自身或自己的后代设为 parent
		if dao.IsDescendant(params.Id, params.ParentId.Int64) {
			panic(exception.New("不能将自身或子部门设为父级，会形成环"))
		}
		parent := dao.SelectOneById(params.ParentId.Int64)
		if parent == nil {
			panic(exception.New("父级部门不存在"))
		}
		department.Parent = parent
	}
	if params.Extend.Valid {
		department.Extend.PutAll(params.Extend.Map)
	}
	department.UpdateDt.Set(time.Now())
	dao.UpdateObj(department)
}

type DeleteDepartmentParams struct {
	Id int64 `validate:"required"`
}

// DeleteDepartment 角色已与部门解耦（角色为全局对象），此处只需检查部门下是否还有用户。
func DeleteDepartment(id int64) {
	dao := departmentdao.New(departmentdao.OptsNone)
	department := dao.SelectOneById(id)
	if department == nil {
		panic(exception.New("部门不存在"))
	}
	if department.Immutable.Bool {
		panic(exception.New("该部门不可删除"))
	}
	userDao := userdao.New(userdao.OptsNone)
	if userDao.CountFromRootDepart(department.Id) > 0 {
		panic(exception.New("部门下还有用户,不能删除"))
	}
	dao.DeleteById(department.Id)
}

// ListDepartments B16: 用 OptsNone 一次查出全部部门，在内存中构建树，
// 替代 OptsAll 的 N+1 递归查询。
func ListDepartments() []*model.Department {
	dao := departmentdao.New(departmentdao.OptsNone)
	list := dao.ListAll()
	return buildDeptTree(list)
}

// buildDeptTree 将扁平的部门列表构建为树形结构。
// Parent 字段仅保留 Id（来自 FK scan），不设完整对象，避免 JSON 序列化循环引用。
func buildDeptTree(list model.DeptList) []*model.Department {
	byId := make(map[int64]*model.Department, len(list))
	for _, d := range list {
		d.Children = nil
		byId[d.Id] = d
	}
	var roots []*model.Department
	for _, d := range list {
		if d.Parent != nil && d.Parent.Id > 0 {
			if parent, ok := byId[d.Parent.Id]; ok {
				parent.Children = append(parent.Children, d)
			} else {
				roots = append(roots, d)
			}
		} else {
			roots = append(roots, d)
		}
	}
	return roots
}
