package model

import (
	"os"
	"time"
)

// FileInfo 文件元信息，供列表接口返回。
// Path 为相对 project.dir 的路径，可直接传回 del 接口。
type FileInfo struct {
	Name    string    `json:"name" comment:"文件名"`
	Path    string    `json:"path" comment:"相对项目目录的路径"`
	Size    int64     `json:"size" comment:"字节"`
	ModTime time.Time `json:"modTime" comment:"修改时间"`
	IsDir   bool      `json:"isDir"`
}

// FromOsInfo 由 os.FileInfo 构造，path 由调用方按项目目录内的相对路径传入。
func FromOsInfo(path string, fi os.FileInfo) FileInfo {
	return FileInfo{
		Name:    fi.Name(),
		Path:    path,
		Size:    fi.Size(),
		ModTime: fi.ModTime(),
		IsDir:   fi.IsDir(),
	}
}
