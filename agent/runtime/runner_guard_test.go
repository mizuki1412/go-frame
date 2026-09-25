package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/filesystem"
)

// TestResolvePathGuards 验证 resolvePath 的工作区边界语义：
// 空路径→工作区、相对路径拼接、.. 逃逸拒绝、绝对路径放行、子目录内 .. 放行。
func TestResolvePathGuards(t *testing.T) {
	ws := t.TempDir()
	b := &windowsSafeBackend{workspace: ws}

	if p, err := b.resolvePath(""); err != nil || p != ws {
		t.Fatalf("空路径应解析为工作区: %q %v", p, err)
	}
	p, err := b.resolvePath(filepath.Join("a", "b.txt"))
	if err != nil || p != filepath.Join(ws, "a", "b.txt") {
		t.Fatalf("相对路径应拼接工作区: %q %v", p, err)
	}
	if _, err := b.resolvePath(filepath.Join("..", "escape.txt")); err == nil {
		t.Fatal(".. 逃逸应被拒绝")
	}
	if _, err := b.resolvePath(filepath.Join("sub", "..", "ok.txt")); err != nil {
		t.Fatalf("子目录内 .. 回到工作区应放行: %v", err)
	}
	abs := filepath.Join(ws, "x.go")
	if p, err := b.resolvePath(abs); err != nil || p != abs {
		t.Fatalf("绝对路径应原样放行: %q %v", p, err)
	}
}

// TestIsSensitiveFile 验证敏感凭据类文件的判定
func TestIsSensitiveFile(t *testing.T) {
	for _, p := range []string{".env", "conf/.env.local", "cert.pem", "server.key", "keys/ID_RSA", "secrets.yaml"} {
		if !isSensitiveFile(p) {
			t.Fatalf("%s 应判定为敏感文件", p)
		}
	}
	for _, p := range []string{"main.go", "env", "keygen.go", "README.md"} {
		if isSensitiveFile(p) {
			t.Fatalf("%s 不应判定为敏感文件", p)
		}
	}
}

// TestReadRejectsSensitive 验证 Read 对敏感文件的拒绝（逃逸路径的拒绝逻辑相同）
func TestReadRejectsSensitive(t *testing.T) {
	ws := t.TempDir()
	b := &windowsSafeBackend{workspace: ws}
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("K=v"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := b.Read(context.Background(), &filesystem.ReadRequest{FilePath: ".env"})
	if err == nil || !strings.Contains(err.Error(), "敏感文件") {
		t.Fatalf("读取 .env 应被拒绝: %v", err)
	}
}

// TestAgentsMdFileBackendNotExist 验证 agentsmd 文件后端对缺失文件返回
// os.ErrNotExist 语义（loader 据此静默跳过而非中止）
func TestAgentsMdFileBackendNotExist(t *testing.T) {
	_, err := (agentsmdFileBackend{}).Read(context.Background(), &filesystem.ReadRequest{
		FilePath: filepath.Join(t.TempDir(), "missing.md"),
	})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("缺失文件应返回 os.ErrNotExist 语义: %v", err)
	}
}
