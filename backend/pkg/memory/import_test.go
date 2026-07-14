package memory

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoInternalImports 确保 pkg/memory 下所有文件不 import 任何 internal 包，
// 保持 SDK 零业务依赖，为将来抽取独立 repo 做准备。
func TestNoInternalImports(t *testing.T) {
	// 获取当前包目录
	dir := "."
	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("failed to get abs path: %v", err)
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, absDir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("failed to parse directory: %v", err)
	}

	forbiddenPrefixes := []string{
		"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/",
	}

	for pkgName, pkg := range pkgs {
		for filename, file := range pkg.Files {
			for _, imp := range file.Imports {
				importPath := strings.Trim(imp.Path.Value, "\"")
				for _, prefix := range forbiddenPrefixes {
					if strings.HasPrefix(importPath, prefix) {
						t.Errorf(
							"pkg/memory must not import internal packages (breaks SDK portability):\n"+
								"  file: %s\n"+
								"  import: %s\n"+
								"  package: %s",
							filepath.Base(filename), importPath, pkgName,
						)
					}
				}
			}
		}
	}
}

// TestNoAstUsedAsUnused ensures the ast import is actually used
var _ = ast.ImportSpec{}
