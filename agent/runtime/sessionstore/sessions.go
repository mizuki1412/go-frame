package sessionstore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/cloudwego/eino/schema"
	"github.com/example/go-frame/pkg/class/exception"
)

// SessionStore 基于本地目录的会话历史持久化：
// 每个 session ID 对应 <dir>/<safe(id)>/ 一个目录：
//   - history.json：会话历史（user/assistant/tool 消息）
//   - perf.json：会话性能数据（调用事件数组：每次模型/工具调用的耗时明细，由 PerfRecorder 写入）
type SessionStore struct {
	dir string
}

// NewSessionStore 创建会话历史存储，目录不存在时自动创建
func NewSessionStore(dir string) (*SessionStore, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, exception.New("解析 session 目录失败: " + err.Error())
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, exception.New("创建 session 目录失败: " + err.Error())
	}
	return &SessionStore{dir: abs}, nil
}

// sessionFile 磁盘上的会话记录
type sessionFile struct {
	ID       string            `json:"id"`
	Messages []*schema.Message `json:"messages"`
}

// NewSessionID 生成唯一的会话 ID（8 字节随机数，16 位 hex）
func NewSessionID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", exception.New("生成 session id 失败: " + err.Error())
	}
	return hex.EncodeToString(b), nil
}

// Load 加载指定 ID 的会话历史；不存在时返回 (nil, nil)
func (s *SessionStore) Load(sessionID string) ([]*schema.Message, error) {
	data, err := os.ReadFile(s.path(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, exception.New("读取 session 失败: " + err.Error())
	}
	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, exception.New("解析 session 失败: " + err.Error())
	}
	return sf.Messages, nil
}

// Save 将会话历史原子写入磁盘（<id>/history.json，临时文件 + rename）
func (s *SessionStore) Save(sessionID string, msgs []*schema.Message) error {
	sf := sessionFile{ID: sessionID, Messages: msgs}
	data, err := json.Marshal(sf)
	if err != nil {
		return exception.New("序列化 session 失败: " + err.Error())
	}
	sessionDir := s.sessionDir(sessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return exception.New("创建 session 目录失败: " + err.Error())
	}
	return writeAtomic(sessionDir, ".sess-*", s.path(sessionID), data)
}

// writeAtomic 原子写文件：先写临时文件再 rename，避免半写状态
func writeAtomic(dir, pattern, final string, data []byte) error {
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return exception.New("创建临时文件失败: " + err.Error())
	}
	tmpPath := tmp.Name()

	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return exception.New("写入失败: " + err.Error())
	}
	if err := tmp.Close(); err != nil {
		return exception.New("关闭文件失败: " + err.Error())
	}
	if err := os.Rename(tmpPath, final); err != nil {
		return exception.New("持久化失败: " + err.Error())
	}
	ok = true
	return nil
}

// path 将 session ID 映射为会话目录下的安全历史文件路径（<dir>/<safe(id)>/history.json）
func (s *SessionStore) path(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), "history.json")
}

// sessionDir 会话 ID 对应的目录（<dir>/<safe(id)>），不存在时自动创建
func (s *SessionStore) sessionDir(sessionID string) string {
	name := unsafeID.ReplaceAllString(sessionID, "_")
	if name == "" {
		name = "_"
	}
	return filepath.Join(s.dir, name)
}

// perfPath 会话 ID 对应的性能数据文件路径（<dir>/<safe(id)>/perf.json）
func (s *SessionStore) perfPath(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), "perf.json")
}

// SavePerf 将会话的调用事件数组原子写入 perf.json
func (s *SessionStore) SavePerf(sessionID string, p PerfData) error {
	if p == nil {
		return nil
	}
	data, err := json.Marshal(p)
	if err != nil {
		return exception.New("序列化 perf 失败: " + err.Error())
	}
	final := s.perfPath(sessionID)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return exception.New("创建 session 目录失败: " + err.Error())
	}
	return writeAtomic(filepath.Dir(final), ".perf-*", final, data)
}

// LoadPerf 加载会话的调用事件数组；不存在时返回 (nil, nil)
func (s *SessionStore) LoadPerf(sessionID string) (PerfData, error) {
	data, err := os.ReadFile(s.perfPath(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, exception.New("读取 perf 失败: " + err.Error())
	}
	var p PerfData
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, exception.New("解析 perf 失败: " + err.Error())
	}
	return p, nil
}
