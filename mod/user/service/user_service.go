package service

import (
	context2 "context"
	"strings"
	"time"

	"github.com/example/go-frame/mod/user/dao/departmentdao"
	"github.com/example/go-frame/mod/user/dao/roledao"
	"github.com/example/go-frame/mod/user/dao/userdao"
	"github.com/example/go-frame/mod/user/dao/userroledao"
	"github.com/example/go-frame/mod/user/model"
	"github.com/example/go-frame/pkg/class"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/library/cryptokit"
	"github.com/example/go-frame/pkg/library/stringkit"
	"github.com/example/go-frame/pkg/service/rediskit"
	"github.com/example/go-frame/pkg/service/sqlkit"
)

// Login 合并 loginByUsername 和 login，返回用户。
// 调用方负责创建 JWT、设置 cookie、调用 AdditionLoginFunc 等 HTTP 层逻辑。
// P1 修复（MD5→bcrypt 迁移）：改为取回用户后在 Go 侧校验密码——
// bcrypt 哈希无法用 DB 等值查询匹配；校验兼容存量 MD5，命中即惰性升级为 bcrypt 重存。
func Login(username, phone, pwd string) *model.User {
	if stringkit.IsNull(username) && stringkit.IsNull(phone) {
		panic(exception.New("用户名或手机号缺失"))
	}
	username = strings.TrimSpace(username)
	phone = strings.TrimSpace(phone)
	dao := userdao.New(userdao.OptsDefault)
	var user *model.User
	if !stringkit.IsNull(username) {
		user = dao.FindByUsername(username)
	} else {
		user = dao.FindByPhone(phone)
	}
	if user == nil || !cryptokit.CheckPwd(pwd, user.Pwd.String) {
		panic(exception.New("账号和密码不匹配"))
	}
	if cryptokit.NeedUpgrade(user.Pwd.String) {
		// 惰性升级：存量 MD5 密码在登录成功时改存为 bcrypt
		user.Pwd.Set(cryptokit.HashPwd(pwd))
		user.UpdateDt.Set(time.Now())
		dao.UpdateObj(user)
	}
	if user.Status.Int32 == model.UserStatusFreeze {
		panic(exception.New("账户被冻结"))
	}
	return user
}

func GetUserById(uid int64) *model.User {
	dao := userdao.New(userdao.OptsDefault)
	return dao.SelectOneById(uid)
}

func UpdatePwd(uid int64, oldPwd, newPwd string) {
	dao := userdao.New(userdao.OptsDefault)
	user := dao.SelectOneById(uid)
	if user == nil { // B1: nil check
		panic(exception.New("用户不存在"))
	}
	if !cryptokit.CheckPwd(oldPwd, user.Pwd.String) {
		panic(exception.New("原密码错误"))
	}
	user.Pwd.Set(cryptokit.HashPwd(newPwd))
	user.UpdateDt.Set(time.Now())
	dao.UpdateObj(user)
}

type UpdateUserInfoParams struct {
	Username   class.String
	Name       class.String
	Phone      class.String
	Sms        class.String
	Gender     int8
	Image      class.String
	Address    class.String
	OldPwd     class.String
	NewPwd     class.String
	ExtendJson class.MapString
}

// UpdateUserInfo 用户自助修改信息。
// B3: 用 SelectOneById 而非 SelectOneWithDelById，不允许修改已删除用户。
// B4: 改密码分支增加 nil check。
func UpdateUserInfo(uid int64, params UpdateUserInfoParams) {
	dao := userdao.New(userdao.OptsNone)
	u := dao.SelectOneById(uid)
	if u == nil { // B1: nil check
		panic(exception.New("用户不存在"))
	}
	if params.Phone.Valid && params.Phone.String != "" && params.Phone.String != u.Phone.String {
		if dao.FindByPhone(params.Phone.String) != nil {
			panic(exception.New("手机号已被注册"))
		}
		if !params.Sms.Valid || rediskit.Get(context2.Background(), rediskit.GetKeyWithPrefix("sms:"+params.Phone.String), "") != params.Sms.String {
			panic(exception.New("验证码错误"))
		}
	}
	if params.Username.Valid && params.Username.String != u.Username.String {
		if dao.FindByUsername(params.Username.String) != nil {
			panic(exception.New("该用户名已被使用"))
		}
		u.Username.Set(params.Username.String)
	}
	if params.Image.Valid {
		u.Image.Set(params.Image)
	}
	if params.Name.Valid {
		u.Name.Set(params.Name.String)
	}
	if params.Phone.Valid {
		u.Phone.Set(params.Phone.String)
	}
	if params.Gender != 0 {
		u.Gender.Set(params.Gender)
	}
	if params.Address.Valid {
		u.Address.Set(params.Address.String)
	}
	if params.ExtendJson.Valid {
		u.Extend.PutAll(params.ExtendJson.Map)
	}
	if params.OldPwd.Valid && params.NewPwd.Valid && params.OldPwd.String != "" && params.NewPwd.String != "" {
		user := dao.SelectOneById(u.Id)
		if user == nil { // B4: nil check
			panic(exception.New("用户不存在"))
		}
		if !cryptokit.CheckPwd(params.OldPwd.String, user.Pwd.String) {
			panic(exception.New("原密码错误"))
		}
		user.Pwd.Set(cryptokit.HashPwd(params.NewPwd.String))
	}
	u.UpdateDt.Set(time.Now())
	dao.UpdateObj(u)
}

type ListUsersParams struct {
	DepartmentIds []int64
	RoleIds       []int64
}

// ListUsers 管理员查看用户列表。
func ListUsers(params ListUsersParams) []*model.User {
	dao := userdao.New(userdao.OptsDefault)
	return dao.List(userdao.ListParam{Roles: params.RoleIds, Departments: params.DepartmentIds})
}

type AddUserParams struct {
	Username class.String `validate:"required"`
	Pwd      class.String `validate:"required"`
	// Roles 角色 id 列表（多角色），经 sys_user_role 中间表绑定
	Roles      class.ArrInt
	Department class.Int64
	Name       class.String
	Phone      class.String
	Sms        class.String
	Gender     int8
	Image      class.String
	Address    class.String
	ExtendJson class.MapString
}

// AddUser 新增用户，角色绑定与用户插入在同一事务内完成。
// B7: 不复用已删除用户记录，避免脏数据。
func AddUser(params AddUserParams, checkSms bool) *model.User {
	dao := userdao.New(userdao.OptsNone)
	if dao.FindByUsername(params.Username.String) != nil {
		panic(exception.New("用户名已经存在"))
	}
	if params.Phone.Valid && dao.FindByPhone(params.Phone.String) != nil {
		panic(exception.New("手机号已经存在"))
	}
	if params.Phone.Valid && checkSms && (!params.Sms.Valid || rediskit.Get(context2.Background(), rediskit.GetKeyWithPrefix("sms:"+params.Phone.String), "") != params.Sms.String) {
		panic(exception.New("验证码错误"))
	}
	roles := loadRoles(params.Roles.Array) // 校验角色存在性，非法 id 直接报错
	u := &model.User{}
	u.CreateDt.Set(time.Now())
	u.Roles = roles
	if params.Department.IsValid() {
		deptDao := departmentdao.New(departmentdao.OptsNone)
		dept := deptDao.SelectOneById(params.Department.Int64)
		if dept == nil {
			panic(exception.New("部门不存在"))
		}
		u.Department = dept
	}
	if params.Username.Valid {
		u.Username.Set(params.Username)
	}
	if params.Pwd.Valid {
		u.Pwd.Set(cryptokit.HashPwd(params.Pwd.String))
	}
	if params.Name.Valid {
		u.Name.Set(params.Name)
	}
	if params.Phone.Valid {
		u.Phone.Set(params.Phone)
	}
	if params.Image.Valid {
		u.Image.Set(params.Image)
	}
	if params.Gender != 0 {
		u.Gender.Set(params.Gender)
	}
	if params.Address.Valid {
		u.Address.Set(params.Address)
	}
	if params.ExtendJson.Valid {
		u.Extend.PutAll(params.ExtendJson.Map)
	}
	// 用户行与角色绑定必须原子：InsertObj 回填自增 id 后才能写中间表
	sqlkit.TxArea(func(targetDS *sqlkit.DataSource) {
		dao1 := userdao.New(userdao.OptsNone, targetDS)
		dao1.InsertObj(u)
		userroledao.New(userroledao.OptsNone, targetDS).ReplaceForUser(u.Id, params.Roles.Array)
	})
	return u
}

// loadRoles 批量校验并加载角色，任一 id 不存在则报错。
func loadRoles(roleIds []int64) []*model.Role {
	if len(roleIds) == 0 {
		return nil
	}
	roles := roledao.New(roledao.OptsNone).SelectByIds(roleIds)
	if len(roles) != len(roleIds) {
		panic(exception.New("角色不存在"))
	}
	return roles
}

type UpdateUserParams struct {
	Id         int64 `validate:"required"`
	Username   class.String
	Name       class.String
	Phone      class.String
	Gender     int8
	Image      class.String
	Address    class.String
	Pwd        class.String
	Department class.Int64
	// Roles 角色 id 列表；Valid=true 且空数组表示清空全部角色
	Roles      class.ArrInt
	ExtendJson class.MapString
}

// UpdateUser 管理员修改用户。
func UpdateUser(params UpdateUserParams) {
	dao := userdao.New(userdao.OptsDefault)
	u := dao.SelectOneById(params.Id)
	if u == nil {
		panic(exception.New("用户不存在"))
	}
	// B9: 内置超级管理员角色（id=0）不允许通过管理员接口改绑
	if u.HasRole(model.RoleIdSuperAdmin) {
		panic(exception.New("该用户不能设置"))
	}
	if params.Phone.Valid && params.Phone.String != "" && params.Phone.String != u.Phone.String && dao.FindByPhone(params.Phone.String) != nil {
		panic(exception.New("手机号已存在"))
	}
	if params.Username.Valid && params.Username.String != u.Username.String {
		if dao.FindByUsername(params.Username.String) != nil {
			panic(exception.New("该用户名已被使用"))
		}
		u.Username.Set(params.Username.String)
	}
	// 角色先校验存在性（非法 id 提前报错），再在事务内整体替换绑定
	if params.Roles.Valid {
		loadRoles(params.Roles.Array)
	}
	if params.Department.IsValid() && (u.Department == nil || params.Department.Int64 != u.Department.Id) {
		deptDao := departmentdao.New(departmentdao.OptsNone)
		dept := deptDao.SelectOneById(params.Department.Int64)
		if dept == nil {
			panic(exception.New("部门不存在"))
		}
		u.Department = dept
	}
	if params.Name.Valid {
		u.Name.Set(params.Name.String)
	}
	if params.Phone.Valid {
		u.Phone.Set(params.Phone.String)
	}
	if params.Image.Valid {
		u.Image.Set(params.Image)
	}
	if params.Pwd.Valid && params.Pwd.String != "" {
		u.Pwd.Set(cryptokit.HashPwd(params.Pwd.String))
	}
	if params.Gender != 0 {
		u.Gender.Set(params.Gender)
	}
	if params.Address.Valid {
		u.Address.Set(params.Address.String)
	}
	if params.ExtendJson.Valid {
		u.Extend.PutAll(params.ExtendJson.Map)
	}
	u.UpdateDt.Set(time.Now())
	sqlkit.TxArea(func(targetDS *sqlkit.DataSource) {
		dao1 := userdao.New(userdao.OptsNone, targetDS)
		dao1.UpdateObj(u)
		if params.Roles.Valid {
			userroledao.New(userroledao.OptsNone, targetDS).ReplaceForUser(u.Id, params.Roles.Array)
		}
	})
}

type DeleteUserParams struct {
	Id  int64       `validate:"required"`
	Off class.Int32 `validate:"required" comment:"0-删除，1-冻结，2-解冻"`
}

// DeleteUser B10: 用 OptsNone 避免级联查询浪费。
func DeleteUser(operatorUid int64, params DeleteUserParams) {
	if operatorUid == 0 {
		panic(exception.New("登录的用户错误"))
	}
	if operatorUid == params.Id {
		panic(exception.New("不能操作自己"))
	}
	dao := userdao.New(userdao.OptsNone) // B10: OptsNone
	target := dao.SelectOneById(params.Id)
	if target == nil {
		panic(exception.New("用户不存在"))
	}
	// B9: 内置超级管理员角色（id=0）不允许删除/冻结
	if target.HasRole(model.RoleIdSuperAdmin) {
		panic(exception.New("该用户不能设置"))
	}
	if target.Immutable.Bool {
		panic(exception.New("该用户不可删除"))
	}
	sqlkit.TxArea(func(targetDS *sqlkit.DataSource) {
		dao1 := userdao.New(userdao.OptsNone, targetDS) // B10: OptsNone
		urDao := userroledao.New(userroledao.OptsNone, targetDS)
		switch params.Off.Int32 {
		case 0:
			urDao.DeleteByUserId(params.Id)
			dao1.SetNull(params.Id)
			dao1.DeleteById(params.Id)
		case 1:
			dao1.FreezeUser(params.Id, model.UserStatusFreeze)
		case 2:
			dao1.FreezeUser(params.Id, model.UserStatusOK)
		default:
			panic(exception.New("无效的操作类型"))
		}
	})
}
