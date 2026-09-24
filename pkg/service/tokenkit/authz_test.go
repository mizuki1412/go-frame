package tokenkit

import "testing"

func TestHasPrivilegeExact(t *testing.T) {
	p := &Principal{Privileges: []string{"user:add", "role:manage"}}
	if !p.HasPrivilege("user:add") {
		t.Error("应命中精确权限码 user:add")
	}
	if p.HasPrivilege("user:del") {
		t.Error("未授予的权限码不应命中")
	}
}

func TestHasPrivilegeWildcard(t *testing.T) {
	tests := []struct {
		name      string
		granted   string
		target    string
		wantMatch bool
	}{
		{"全局通配命中一切", "*", "user:add", true},
		{"全局通配命中部门权限", "*", "department:manage", true},
		{"末段通配命中同模块", "user:*", "user:add", true},
		{"末段通配跨层级命中", "user:*", "user:admin:list", true},
		{"末段通配不跨模块", "user:*", "role:manage", false},
		{"中段通配需段数相同", "*:add", "user:add", true},
		{"中段通配不跨深度", "*:add", "user:admin:add", false},
		{"中段通配首段固定", "user:*:list", "user:admin:list", true},
		{"中段通配首段不符", "user:*:list", "role:admin:list", false},
		{"多段全通配", "*:*", "a:b", true},
		{"无通配不匹配", "user", "user:add", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Principal{Privileges: []string{tt.granted}}
			if got := p.HasPrivilege(tt.target); got != tt.wantMatch {
				t.Errorf("HasPrivilege(%q) with granted %q = %v, want %v",
					tt.target, tt.granted, got, tt.wantMatch)
			}
		})
	}
}

// exclude 的优先级必须高于一切授予，包括全局通配。
func TestExcludeBeatsGranted(t *testing.T) {
	tests := []struct {
		name      string
		privs     []string
		exclude   []string
		target    string
		wantMatch bool
	}{
		{"未剔除时命中", []string{"user:*"}, nil, "user:add", true},
		{"剔除具体码后不命中", []string{"user:*"}, []string{"user:add"}, "user:add", false},
		{"剔除其他码仍命中", []string{"user:*"}, []string{"user:del"}, "user:add", true},
		{"剔除星号全禁", []string{"*"}, []string{"*"}, "user:add", false},
		{"空权限码不匹配", []string{"user:*"}, nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Principal{Privileges: tt.privs, Exclude: tt.exclude}
			if got := p.HasPrivilege(tt.target); got != tt.wantMatch {
				t.Errorf("HasPrivilege(%q) = %v, want %v", tt.target, got, tt.wantMatch)
			}
		})
	}
}

func TestHasPermOrSemantics(t *testing.T) {
	p := &Principal{Privileges: []string{"user:add"}}
	if !p.HasPerm("role:manage", "user:add") {
		t.Error("OR 语义下命中其一即应通过")
	}
	if p.HasPerm("role:manage", "user:del") {
		t.Error("OR 语义下都不命中应拒绝")
	}
	if p.HasPerm() {
		t.Error("空条件不应放行")
	}
}

// 未装配权限数据源时 principal 为 nil，所有判定必须拒绝（fail closed）。
func TestNilPrincipalDenies(t *testing.T) {
	var p *Principal
	if p.HasPrivilege("user:add") {
		t.Error("nil principal 不应有任何权限")
	}
	if p.HasPerm("*") {
		t.Error("nil principal 不应被全局通配放行")
	}
	if p.HasRole(1) {
		t.Error("nil principal 不应命中角色")
	}
	if p.HasDept(1) {
		t.Error("nil principal 不应命中部门")
	}
}

func TestHasRole(t *testing.T) {
	p := &Principal{Roles: []int64{2, 5}}
	if !p.HasRole(5) {
		t.Error("应命中持有的角色 5")
	}
	if !p.HasRole(1, 2) {
		t.Error("OR 语义下命中其一即应通过")
	}
	if p.HasRole(1, 3) {
		t.Error("都不命中应拒绝")
	}
	if p.HasRole() {
		t.Error("空条件不应放行")
	}
}

func TestHasDept(t *testing.T) {
	p := &Principal{Department: 7}
	if !p.HasDept(7) {
		t.Error("应命中所属部门")
	}
	if !p.HasDept(1, 7) {
		t.Error("OR 语义下命中其一即应通过")
	}
	if p.HasDept(1, 2) {
		t.Error("都不命中应拒绝")
	}
	// 无部门的用户不应通过任何部门校验
	none := &Principal{}
	if none.HasDept(0) {
		t.Error("无部门用户不应通过部门校验")
	}
}

func TestIsDeptUnder(t *testing.T) {
	// 7 是根，子树为 [8,9,10]（调用方已展开好的完整子树）
	subtree := []int64{8, 9, 10}
	if !(&Principal{Department: 7}).IsDeptUnder(7, subtree) {
		t.Error("本部门应命中自身")
	}
	if !(&Principal{Department: 9}).IsDeptUnder(7, subtree) {
		t.Error("子部门应命中")
	}
	if (&Principal{Department: 99}).IsDeptUnder(7, subtree) {
		t.Error("子树外的部门不应命中")
	}
	if (&Principal{Department: 8}).IsDeptUnder(9, nil) {
		t.Error("未传子树时只应命中自身")
	}
}

func TestIsSuperAdmin(t *testing.T) {
	p := &Principal{Roles: []int64{0, 3}}
	if !p.IsSuperAdmin(0) {
		t.Error("持有内置超级管理员角色应返回 true")
	}
	if p.IsSuperAdmin(1) {
		t.Error("未持有指定角色应返回 false")
	}
}
