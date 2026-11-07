package service

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/example/go-frame/mod/common/model"
	"github.com/example/go-frame/pkg/class"
	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/library/cryptokit"
	"github.com/example/go-frame/pkg/library/filekit"
	"github.com/example/go-frame/pkg/service/storagekit"
)

// 路径安全基线：所有接口只允许操作 project.dir 下的 common/ 目录，
// 拒绝绝对路径、盘符、.. 上跳，防止 ../ 穿越读写任意文件。
const commonDir = "common"

// safeRelPath 校验 name/path 为 common/ 下的相对路径并返回项目目录内的相对路径。
// 非法（空、绝对路径、..、盘符）一律报错；返回值已归一为 / 分隔。
func safeRelPath(name string) string {
	if strings.TrimSpace(name) == "" {
		panic(exception.New("文件名不能为空"))
	}
	if filepath.IsAbs(name) || strings.ContainsRune(name, '\\') ||
		strings.Contains(name, "..") || strings.Contains(name, ":") {
		panic(exception.New("非法文件路径"))
	}
	rel := filepath.ToSlash(filepath.Join(commonDir, filepath.ToSlash(name)))
	// Join 后必须仍以 common/ 开头，杜绝任何绕过
	if !strings.HasPrefix(rel, commonDir+"/") {
		panic(exception.New("非法文件路径"))
	}
	return rel
}

// Upload 上传文件到 common/ 下（public 或 private 子目录）。
// 存储名用 xid 生成避免客户端可控路径与重名覆盖，保留原始扩展名。
func Upload(file *class.File, public bool, folder string) string {
	sub := "private"
	if public {
		sub = "public"
	}
	rel := safeRelPath(sub + "/" + folder + "/" + cryptokit.ID() + extOf(file.Header.Filename))
	storagekit.SaveInHome(file, rel)
	return rel
}

// extOf 取小写扩展名（含点），无扩展名时返回空串。
func extOf(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	// 扩展名本身也须通过安全校验（防 ".%2e%2e" 之类），含路径分隔符即拒绝
	if strings.ContainsAny(ext, "/\\") {
		panic(exception.New("非法文件扩展名"))
	}
	return ext
}

// List 列出 common/ 下（含子目录）的文件，按修改时间倒序。
func List() []model.FileInfo {
	root := storagekit.GetFullPath(commonDir)
	if !filekit.Exists(root) {
		return []model.FileInfo{}
	}
	paths := filekit.ListFileNames(root)
	list := make([]model.FileInfo, 0, len(paths))
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			continue
		}
		list = append(list, model.FromOsInfo(commonDir+"/"+filepath.ToSlash(rel), fi))
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ModTime.After(list[j].ModTime) })
	return list
}

// Resolve 校验请求路径并返回磁盘全路径，供下载/读取。
func Resolve(relName string) string {
	rel := safeRelPath(relName)
	full := storagekit.GetFullPath(rel)
	if !filekit.Exists(full) {
		panic(exception.New("文件不存在"))
	}
	return full
}

// Delete 删除 common/ 下单个文件。文件由管理员接口（del 权限）触发，
// storagekit 的根目录校验已在 safeRelPath 内完成。
func Delete(relName string) {
	rel := safeRelPath(relName)
	full := storagekit.GetFullPath(rel)
	if !filekit.Exists(full) {
		panic(exception.New("文件不存在"))
	}
	filekit.RemoveFile(full)
}
