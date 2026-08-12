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

func TestProductionDependencyBoundaries(t *testing.T) {
	assertProductionDependencyBoundaries(t)
}

func assertProductionDependencyBoundaries(t *testing.T) {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate dependency test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", ".."))
	harnessRoot := filepath.Join(repositoryRoot, "internal", "harness")
	fileSet := token.NewFileSet()
	violations := make([]string, 0)

	err := filepath.WalkDir(harnessRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(repositoryRoot, path)
		if err != nil {
			return err
		}
		directory := filepath.ToSlash(filepath.Dir(relative))
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if reason := forbiddenImport(directory, importPath); reason != "" {
				position := fileSet.Position(spec.Pos())
				violations = append(violations, position.String()+": "+reason+" "+strconv.Quote(importPath))
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.IfStmt:
				appendScriptedViolation(fileSet, path, node.Cond, "ScriptedModel branch", &violations)
			case *ast.SwitchStmt:
				appendScriptedViolation(fileSet, path, node.Tag, "ScriptedModel switch", &violations)
			case *ast.TypeSwitchStmt:
				appendScriptedViolation(fileSet, path, node.Assign, "ScriptedModel type switch", &violations)
			case *ast.CaseClause:
				for _, expression := range node.List {
					appendScriptedViolation(fileSet, path, expression, "ScriptedModel case", &violations)
				}
			case *ast.TypeAssertExpr:
				appendScriptedViolation(fileSet, path, node.Type, "ScriptedModel type assertion", &violations)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("production dependency boundary violations:\n%s", strings.Join(violations, "\n"))
	}
}

const modulePath = "github.com/SongYii/open-code-harness"

func forbiddenImport(directory, importPath string) string {
	domainDir := "internal/harness/domain"
	engineDir := "internal/harness/engine"
	applicationDir := "internal/harness/application"
	memoryDir := "internal/harness/adapters/memory"

	forbidden := make([]string, 0, 8)
	switch directory {
	case domainDir:
		forbidden = append(forbidden,
			modulePath+"/internal/harness/application",
			modulePath+"/internal/harness/engine",
			modulePath+"/internal/harness/adapters",
			modulePath+"/internal/harness/testkit",
		)
	case engineDir:
		forbidden = append(forbidden,
			modulePath+"/internal/harness/application",
			modulePath+"/internal/harness/adapters",
			modulePath+"/internal/harness/testkit",
		)
	case applicationDir:
		forbidden = append(forbidden,
			modulePath+"/internal/harness/adapters",
			modulePath+"/internal/harness/testkit",
		)
	}
	for _, prefix := range forbidden {
		if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
			return "forbidden package dependency"
		}
	}
	if directory == domainDir || directory == engineDir {
		for _, segment := range []string{"acp", "mcp", "tui", "provider", "providers"} {
			if hasPathSegment(importPath, segment) {
				return "forbidden protocol/provider dependency"
			}
		}
	}
	if directory == applicationDir || directory == engineDir || directory == memoryDir {
		switch importPath {
		case "os", "os/exec", "net", "net/http":
			return "forbidden host/network dependency"
		}
	}
	return ""
}

func hasPathSegment(path, wanted string) bool {
	for _, segment := range strings.Split(path, "/") {
		if strings.EqualFold(segment, wanted) {
			return true
		}
	}
	return false
}

func appendScriptedViolation(fileSet *token.FileSet, path string, node ast.Node, context string, violations *[]string) {
	if node == nil || !containsScriptedModel(node) {
		return
	}
	position := fileSet.Position(node.Pos())
	if position.Filename == "" {
		position.Filename = path
	}
	*violations = append(*violations, position.String()+": "+context)
}

func containsScriptedModel(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		if found {
			return false
		}
		switch node := node.(type) {
		case *ast.Ident:
			found = node.Name == "ScriptedModel"
		case *ast.SelectorExpr:
			found = node.Sel != nil && node.Sel.Name == "ScriptedModel"
		}
		return !found
	})
	return found
}
