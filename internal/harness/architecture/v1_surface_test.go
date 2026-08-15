package architecture_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNoEventStoreV1Surface(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate architecture test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", ".."))
	harnessRoot := filepath.Join(repositoryRoot, "internal", "harness")
	fileSet := token.NewFileSet()
	var violations []string

	err := filepath.WalkDir(harnessRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, relErr := filepath.Rel(repositoryRoot, path)
		if relErr != nil {
			return relErr
		}
		if !shouldInspectProductionDirectory(filepath.ToSlash(filepath.Dir(relative))) {
			return nil
		}
		parsed, parseErr := parser.ParseFile(fileSet, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.Ident:
				if node.Name == "EventStoreV2" || node.Name == "AppendRequestV2" {
					violations = append(violations, formatPos(fileSet, path, node.Pos())+": temporary name "+node.Name)
				}
			case *ast.TypeSpec:
				if node.Name != nil && node.Name.Name == "Session" {
					if structType, ok := node.Type.(*ast.StructType); ok {
						for _, field := range structType.Fields.List {
							for _, name := range field.Names {
								if name.Name == "Turns" || name.Name == "TurnOrder" {
									violations = append(violations, formatPos(fileSet, path, name.Pos())+": Session."+name.Name)
								}
							}
						}
					}
				}
			case *ast.FuncDecl:
				if node.Name != nil && node.Recv != nil && len(node.Recv.List) == 1 {
					recv := typeName(node.Recv.List[0].Type)
					if recv == "EventStore" && node.Name.Name == "Load" && isV1LoadSignature(node.Type) {
						violations = append(violations, formatPos(fileSet, path, node.Name.Pos())+": EventStore.Load v1 stream signature")
					}
					if node.Name.Name == "Append" && returnsRecordedEventSlice(node.Type) {
						violations = append(violations, formatPos(fileSet, path, node.Name.Pos())+": Append returns []domain.RecordedEvent")
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("v1/temporary EventStore surfaces remain:\n%s", strings.Join(violations, "\n"))
	}
}

func formatPos(fileSet *token.FileSet, path string, pos token.Pos) string {
	position := fileSet.Position(pos)
	if position.Filename == "" {
		position.Filename = path
	}
	return position.String()
}

func typeName(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name
	case *ast.StarExpr:
		return typeName(expr.X)
	case *ast.IndexExpr:
		return typeName(expr.X)
	default:
		return fmt.Sprintf("%T", expr)
	}
}

func isV1LoadSignature(fn *ast.FuncType) bool {
	if fn == nil || fn.Params == nil || fn.Results == nil || len(fn.Params.List) != 2 || len(fn.Results.List) != 2 {
		return false
	}
	return returnsRecordedEventSlice(fn)
}

func returnsRecordedEventSlice(fn *ast.FuncType) bool {
	if fn == nil || fn.Results == nil || len(fn.Results.List) < 1 {
		return false
	}
	array, ok := fn.Results.List[0].Type.(*ast.ArrayType)
	if !ok || array.Elt == nil {
		return false
	}
	sel, ok := array.Elt.(*ast.SelectorExpr)
	return ok && sel.Sel != nil && sel.Sel.Name == "RecordedEvent"
}
