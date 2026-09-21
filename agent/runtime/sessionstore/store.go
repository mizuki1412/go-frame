package sessionstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/example/go-frame/pkg/class/exception"
)

// FileStore 基于本地目录的 CheckPointStore 实现：
// 每个 checkPointID 对应 <dir>/<safe(id)> 一个文件，内容为 ADK 序列化的 checkpoint 字节。
//
// 同时实现 adk.CheckPointStore（Get/Set）与 adk.CheckPointDeleter（Delete）。
// 并发安全：所有文件操作持同一把锁（ponytail: 全局锁，单进程 CLI 场景足够）。
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore 创建目录版 CheckPointStore，目录不存在时自动创建。
func NewFileStore(dir string) (*FileStore, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, exception.New("解析 checkpoint 目录失败: " + err.Error())
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, exception.New("创建 checkpoint 目录失败: " + err.Error())
	}
	return &FileStore{dir: abs}, nil
}

// unsafeID 匹配 Windows/跨平台非法文件名字符
var unsafeID = regexp.MustCompile(`[\\/:*?"<>|]`)

// safePath 将任意 checkPointID 映射为目录下安全文件名
func (s *FileStore) safePath(checkPointID string) string {
	name := unsafeID.ReplaceAllString(checkPointID, "_")
	if name == "" {
		name = "_"
	}
	return filepath.Join(s.dir, name)
}

// Get 实现 adk.CheckPointStore。checkpoint 不存在时返回 (nil, false, nil)。
func (s *FileStore) Get(_ context.Context, checkPointID string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.safePath(checkPointID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, exception.New("读取 checkpoint 失败: " + err.Error())
	}
	return data, true, nil
}

// Set 实现 adk.CheckPointStore。先写临时文件再 rename，避免半写状态。
func (s *FileStore) Set(_ context.Context, checkPointID string, checkPoint []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	final := s.safePath(checkPointID)
	tmp, err := os.CreateTemp(s.dir, ".ckpt-*")
	if err != nil {
		return exception.New("创建 checkpoint 临时文件失败: " + err.Error())
	}
	tmpPath := tmp.Name()

	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(checkPoint); err != nil {
		return exception.New("写入 checkpoint 失败: " + err.Error())
	}
	if err := tmp.Close(); err != nil {
		return exception.New("关闭 checkpoint 文件失败: " + err.Error())
	}
	if err := os.Rename(tmpPath, final); err != nil {
		return exception.New("持久化 checkpoint 失败: " + err.Error())
	}
	ok = true
	return nil
}

// Delete 实现 adk.CheckPointDeleter。不存在视为成功（幂等）。
func (s *FileStore) Delete(_ context.Context, checkPointID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := os.Remove(s.safePath(checkPointID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return exception.New("删除 checkpoint 失败: " + err.Error())
	}
	return nil
}

// 编译期断言：FileStore 满足 CheckPointStore 与 CheckPointDeleter 接口
var (
	_ adk.CheckPointStore   = (*FileStore)(nil)
	_ adk.CheckPointDeleter = (*FileStore)(nil)
)
