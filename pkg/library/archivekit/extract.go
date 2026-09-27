// Package archivekit 归档工具集：zip 与 tar 系（tar / tar.gz / tgz / tar.bz2 / tbz2）
// 压缩包的识别与安全解压。解压内置两类防护：条目路径逃逸（zip slip）与
// 解压炸弹（总量/条目数上限）；rar/7z/tar.xz 需第三方解码库，不支持。
// 打包能力（tar.gz / zip）按设计暂未提供，待有实际使用场景后再新增。
package archivekit

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/example/go-frame/pkg/class/exception"
	"github.com/example/go-frame/pkg/service/logkit"
)

const (
	// maxExtractBytes 单个压缩包解压后的总字节上限（防解压炸弹）
	maxExtractBytes = 4 << 30
	// maxExtractEntries 单个压缩包的条目数上限
	maxExtractEntries = 20000
)

// Kind 按文件名（大小写不敏感）识别压缩包类型：zip / tar / targz / tarbz2，非压缩包返回空串
func Kind(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "targz"
	case strings.HasSuffix(lower, ".tar.bz2"), strings.HasSuffix(lower, ".tbz2"):
		return "tarbz2"
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	case strings.HasSuffix(lower, ".tar"):
		return "tar"
	}
	return ""
}

// IsArchive 文件名是否为可解压的压缩包
func IsArchive(name string) bool {
	return Kind(name) != ""
}

// Extract 按文件名识别压缩包类型并解压到 destDir（自动创建）
func Extract(srcPath, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return exception.New("创建解压目录失败: " + err.Error())
	}
	switch Kind(filepath.Base(srcPath)) {
	case "zip":
		return extractZipFile(srcPath, destDir)
	case "tar", "targz", "tarbz2":
		f, err := os.Open(srcPath)
		if err != nil {
			return exception.New("打开压缩包失败: " + err.Error())
		}
		defer f.Close()
		var r io.Reader = f
		switch Kind(filepath.Base(srcPath)) {
		case "targz":
			zr, err := gzip.NewReader(f)
			if err != nil {
				return exception.New("gzip 流读取失败: " + err.Error())
			}
			defer zr.Close()
			r = zr
		case "tarbz2":
			r = bzip2.NewReader(f)
		}
		return extractTar(r, destDir)
	}
	return exception.New("不支持的压缩包类型: " + filepath.Base(srcPath))
}

// ── zip 解压 ────────────────────────────────────────────────────────

// ExtractZipBytes 解压内存中的 zip 字节到 destDir（自动创建）
func ExtractZipBytes(data []byte, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return exception.New("创建解压目录失败: " + err.Error())
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return exception.New("zip 数据打开失败: " + err.Error())
	}
	return extractZip(zr, destDir)
}

func extractZipFile(srcPath, destDir string) error {
	zr, err := zip.OpenReader(srcPath)
	if err != nil {
		return exception.New("压缩包打开失败: " + err.Error())
	}
	defer zr.Close()
	return extractZip(&zr.Reader, destDir)
}

func extractZip(r *zip.Reader, destDir string) error {
	var total uint64
	for _, f := range r.File {
		total += f.UncompressedSize64
		if total > maxExtractBytes {
			return exception.New("压缩包解压后超过大小上限，疑似解压炸弹，已中止")
		}
	}
	if len(r.File) > maxExtractEntries {
		return exception.New("压缩包条目数超过上限，疑似解压炸弹，已中止")
	}
	for _, f := range r.File {
		dest, ok := safeJoin(destDir, f.Name)
		if !ok {
			logkit.Info("[archivekit] 压缩包条目路径不安全，跳过", "entry", f.Name)
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			logkit.Info("[archivekit] 压缩包符号链接条目跳过", "entry", f.Name)
			continue
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return exception.New("创建解压目录失败: " + err.Error())
			}
			continue
		}
		if err := writeZipEntry(f, dest); err != nil {
			return err
		}
	}
	return nil
}

func writeZipEntry(f *zip.File, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return exception.New("创建解压目录失败: " + err.Error())
	}
	src, err := f.Open()
	if err != nil {
		return exception.New("读取压缩包条目失败: " + err.Error())
	}
	defer src.Close()
	dst, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode().Perm()&0o755|0o400)
	if err != nil {
		return exception.New("写出解压文件失败: " + err.Error())
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return exception.New("写出解压文件失败: " + err.Error())
	}
	return nil
}

// ── tar 解压 ────────────────────────────────────────────────────────

// extractTar 从 tar 流解压到 destDir（目录/普通文件，符号链接等特殊条目跳过）
func extractTar(r io.Reader, destDir string) error {
	tr := tar.NewReader(r)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return exception.New("tar 流读取失败: " + err.Error())
		}
		total += hdr.Size
		if total > maxExtractBytes {
			return exception.New("压缩包解压后超过大小上限，疑似解压炸弹，已中止")
		}
		dest, ok := safeJoin(destDir, hdr.Name)
		if !ok {
			logkit.Info("[archivekit] 压缩包条目路径不安全，跳过", "entry", hdr.Name)
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return exception.New("创建解压目录失败: " + err.Error())
			}
		case tar.TypeReg:
			if err := writeTarEntry(tr, dest, fs.FileMode(hdr.Mode)); err != nil {
				return err
			}
		default:
			// 符号链接、硬链接等条目一律跳过
			logkit.Info("[archivekit] 压缩包特殊条目跳过", "entry", hdr.Name, "type", string(hdr.Typeflag))
		}
	}
}

func writeTarEntry(tr *tar.Reader, dest string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return exception.New("创建解压目录失败: " + err.Error())
	}
	dst, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm()&0o755|0o400)
	if err != nil {
		return exception.New("写出解压文件失败: " + err.Error())
	}
	defer dst.Close()
	if _, err := io.Copy(dst, tr); err != nil {
		return exception.New("写出解压文件失败: " + err.Error())
	}
	return nil
}

// safeJoin 校验压缩包内条目路径，拒绝绝对路径与 .. 逃逸（zip slip），返回目标绝对路径
func safeJoin(destDir, name string) (string, bool) {
	cleaned := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(cleaned) {
		return "", false
	}
	dest := filepath.Join(destDir, cleaned)
	rel, err := filepath.Rel(destDir, dest)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return dest, true
}
