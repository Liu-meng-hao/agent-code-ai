package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeFile 在测试用的临时仓库中写入文件（自动创建父目录）。
func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}
}

func TestModulePath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module testmod\n\ngo 1.26\n")
	if got := modulePath(root); got != "testmod" {
		t.Errorf("modulePath = %q, 期望 testmod", got)
	}
	if got := modulePath(filepath.Join(root, "not-exist")); got != "" {
		t.Errorf("无 go.mod 时应返回空串，实际 %q", got)
	}
}

func TestAffectedFilesReverseDeps(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module testmod\n\ngo 1.26\n")
	writeFile(t, root, "a/a.go", "package a\n\nfunc F() {}\n")
	// b 直接 import 了变更包 testmod/a → 受影响
	writeFile(t, root, "b/b.go", "package b\n\nimport \"testmod/a\"\n\nfunc Use() { a.F() }\n")
	// c 不引用变更包 → 不受影响
	writeFile(t, root, "c/c.go", "package c\n\nfunc Alone() {}\n")

	got := affectedFiles(root, []string{"a/a.go"})
	if len(got) != 1 || got[0] != "b/b.go" {
		t.Fatalf("期望仅 b/b.go 受影响，实际: %v", got)
	}
}

func TestAffectedFilesExcludesSelfTestAndVendor(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module testmod\n\ngo 1.26\n")
	writeFile(t, root, "a/a.go", "package a\n\nfunc F() {}\n")
	// 同目录同包文件不构成反向依赖（不会 import 自己）
	writeFile(t, root, "a/mate.go", "package a\n\nfunc G() {}\n")
	// 测试文件排除
	writeFile(t, root, "b/b_test.go", "package b\n\nimport \"testmod/a\"\n\nfunc Use() { a.F() }\n")
	// vendor 目录整体跳过
	writeFile(t, root, "vendor/x/x.go", "package x\n\nimport \"testmod/a\"\n")

	if got := affectedFiles(root, []string{"a/a.go"}); len(got) != 0 {
		t.Errorf("同包文件/测试文件/vendor 都不应算作受影响文件，实际: %v", got)
	}
}

func TestAffectedFilesCap(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module testmod\n\ngo 1.26\n")
	writeFile(t, root, "a/a.go", "package a\n\nfunc F() {}\n")
	for i := 0; i < maxCrossFileFiles+3; i++ {
		dir := fmt.Sprintf("dep%02d", i)
		writeFile(t, root, dir+"/x.go",
			"package "+dir+"\n\nimport \"testmod/a\"\n\nfunc Use() { a.F() }\n")
	}
	if got := affectedFiles(root, []string{"a/a.go"}); len(got) != maxCrossFileFiles {
		t.Errorf("受影响文件应截断到 %d，实际 %d", maxCrossFileFiles, len(got))
	}
}
