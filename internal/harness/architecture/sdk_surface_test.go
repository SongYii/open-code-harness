package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestSDKExportsNoInternalTypes(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "sdk"))
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		internal := map[string]bool{}
		for _, spec := range parsed.Imports {
			importPath, _ := strconv.Unquote(spec.Path.Value)
			if !strings.Contains(importPath, "/internal/") {
				continue
			}
			name := filepath.Base(importPath)
			if spec.Name != nil {
				name = spec.Name.Name
			}
			if name == "." {
				t.Errorf("%s: dot internal import", path)
			}
			internal[name] = true
		}
		check := func(node ast.Node) {
			ast.Inspect(node, func(node ast.Node) bool {
				if selector, ok := node.(*ast.SelectorExpr); ok {
					if ident, ok := selector.X.(*ast.Ident); ok && internal[ident.Name] {
						t.Errorf("%s: internal type in public API: %s.%s", path, ident.Name, selector.Sel.Name)
					}
				}
				return true
			})
		}
		for _, decl := range parsed.Decls {
			switch value := decl.(type) {
			case *ast.FuncDecl:
				if ast.IsExported(value.Name.Name) {
					check(value.Type)
				}
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					switch item := spec.(type) {
					case *ast.TypeSpec:
						if ast.IsExported(item.Name.Name) {
							check(item.Type)
						}
					case *ast.ValueSpec:
						for _, name := range item.Names {
							if ast.IsExported(name.Name) {
								check(item)
							}
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
