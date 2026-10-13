package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// maxCrossFileFiles 单次跨文件审查最多纳入的受影响文件数，避免 token 与调用成本失控。
const maxCrossFileFiles = 8

// skipDirs 遍历依赖时跳过的目录名（版本库、第三方依赖、构建产物、测试数据）。
var skipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"testdata":     true,
}

// modulePath 从仓库根目录的 go.mod 中解析模块路径；读取失败或没有 module 行时返回空串。
func modulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// importPath 由模块路径与文件所在目录（相对仓库根）换算出该包对外暴露的 import path。
func importPath(mod, dir string) string {
	dir = filepath.ToSlash(strings.TrimPrefix(dir, "./"))
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" || dir == "." {
		return mod
	}
	return mod + "/" + dir
}

// fileImports 用 go/parser 只解析文件的 import 声明（ImportsOnly），返回其导入路径列表。
func fileImports(path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		if p, err := strconv.Unquote(spec.Path.Value); err == nil {
			imports = append(imports, p)
		}
	}
	return imports, nil
}

// affectedFiles 返回「未改动、但直接 import 了某个变更文件所在包」的 Go 文件（一层反向依赖）。
// root 为仓库根目录（生产传 "."）。跳过低价值文件、测试文件与第三方/产物目录；
// 单个文件解析或读取失败只跳过该文件。整体失败（无 go.mod 或无变更包）返回 nil，
// 调用方据此跳过跨文件审查，不影响主流程。
func affectedFiles(root string, changedFiles []string) []string {
	mod := modulePath(root)
	if mod == "" {
		return nil
	}

	// 变更文件的所在包（按 import path 表示）与变更文件集合（用于排除自身）
	changedPkgs := map[string]bool{}
	changedSet := map[string]bool{}
	for _, f := range changedFiles {
		f = filepath.ToSlash(f)
		changedSet[f] = true
		if strings.HasSuffix(f, ".go") {
			changedPkgs[importPath(mod, filepath.Dir(f))] = true
		}
	}
	if len(changedPkgs) == 0 {
		return nil
	}

	var affected []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 单个路径出错跳过，不影响其余
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if changedSet[rel] || isLowValueFile(rel) {
			return nil
		}
		imports, parseErr := fileImports(path)
		if parseErr != nil {
			return nil
		}
		for _, imp := range imports {
			if changedPkgs[imp] {
				affected = append(affected, rel)
				break
			}
		}
		return nil
	})

	sort.Strings(affected)
	if len(affected) > maxCrossFileFiles {
		affected = affected[:maxCrossFileFiles]
	}
	return affected
}
