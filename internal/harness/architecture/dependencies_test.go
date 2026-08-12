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

func TestClassifyProductionDirectory(t *testing.T) {
	tests := []struct {
		name      string
		directory string
		want      packageOwner
		inspect   bool
		hasOwner  bool
	}{
		{name: "domain root", directory: "internal/harness/domain", want: ownerDomain, inspect: true, hasOwner: true},
		{name: "domain production subpackage", directory: "internal/harness/domain/codec/v2", want: ownerDomain, inspect: true, hasOwner: true},
		{name: "engine root", directory: "internal/harness/engine", want: ownerEngine, inspect: true, hasOwner: true},
		{name: "engine production subpackage", directory: "internal/harness/engine/streaming", want: ownerEngine, inspect: true, hasOwner: true},
		{name: "application root", directory: "internal/harness/application", want: ownerApplication, inspect: true, hasOwner: true},
		{name: "application production subpackage", directory: "internal/harness/application/orchestration", want: ownerApplication, inspect: true, hasOwner: true},
		{name: "memory root", directory: "internal/harness/adapters/memory", want: ownerMemory, inspect: true, hasOwner: true},
		{name: "memory production subpackage", directory: "internal/harness/adapters/memory/index", want: ownerMemory, inspect: true, hasOwner: true},
		{name: "scenario test support", directory: "internal/harness/application/enginescenariotest", want: ownerApplication, inspect: false, hasOwner: true},
		{name: "scenario nested test support", directory: "internal/harness/application/enginescenariotest/internal", want: ownerApplication, inspect: false, hasOwner: true},
		{name: "similarly named production child", directory: "internal/harness/application/enginescenariotestkit", want: ownerApplication, inspect: true, hasOwner: true},
		{name: "event store test support", directory: "internal/harness/application/eventstoretest", want: ownerApplication, inspect: false, hasOwner: true},
		{name: "model test support", directory: "internal/harness/engine/modeltest", want: ownerEngine, inspect: false, hasOwner: true},
		{name: "unowned adapter still inspected", directory: "internal/harness/adapters/other", inspect: true},
		{name: "unowned nested adapter still inspected", directory: "internal/harness/adapters/other/internal", inspect: true},
		{name: "harness root still inspected", directory: "internal/harness", inspect: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldInspectProductionDirectory(test.directory); got != test.inspect {
				t.Errorf("shouldInspectProductionDirectory(%q) = %t, want %t", test.directory, got, test.inspect)
			}
			got, hasOwner := packageOwnership(test.directory)
			if got != test.want || hasOwner != test.hasOwner {
				t.Errorf("packageOwnership(%q) = (%q, %t), want (%q, %t)", test.directory, got, hasOwner, test.want, test.hasOwner)
			}
		})
	}
}

func TestForbiddenImport(t *testing.T) {
	tests := []struct {
		name       string
		owner      packageOwner
		importPath string
		forbidden  bool
	}{
		{name: "domain subpackage cannot import application", owner: ownerDomain, importPath: modulePath + "/internal/harness/application", forbidden: true},
		{name: "domain cannot import engine subpackage", owner: ownerDomain, importPath: modulePath + "/internal/harness/engine/streaming", forbidden: true},
		{name: "engine cannot import application subpackage", owner: ownerEngine, importPath: modulePath + "/internal/harness/application/orchestration", forbidden: true},
		{name: "application cannot import memory subpackage", owner: ownerApplication, importPath: modulePath + "/internal/harness/adapters/memory/index", forbidden: true},
		{name: "memory cannot import network", owner: ownerMemory, importPath: "net/http", forbidden: true},
		{name: "application may import engine", owner: ownerApplication, importPath: modulePath + "/internal/harness/engine", forbidden: false},
		{name: "engine may import domain", owner: ownerEngine, importPath: modulePath + "/internal/harness/domain", forbidden: false},
		{name: "domain may import standard library", owner: ownerDomain, importPath: "time", forbidden: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := forbiddenImport(test.owner, test.importPath)
			if (got != "") != test.forbidden {
				t.Fatalf("forbiddenImport(%q, %q) = %q, forbidden=%t", test.owner, test.importPath, got, test.forbidden)
			}
		})
	}
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
		relative, err := filepath.Rel(repositoryRoot, path)
		if err != nil {
			return err
		}
		directory := filepath.ToSlash(filepath.Dir(relative))
		if !shouldInspectProductionDirectory(directory) {
			return nil
		}
		owner, hasOwner := packageOwnership(directory)
		parsed, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			return err
		}
		if hasOwner {
			for _, spec := range parsed.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if reason := forbiddenImport(owner, importPath); reason != "" {
					position := fileSet.Position(spec.Pos())
					violations = append(violations, position.String()+": "+reason+" "+strconv.Quote(importPath))
				}
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

type packageOwner string

const (
	ownerDomain      packageOwner = "domain"
	ownerEngine      packageOwner = "engine"
	ownerApplication packageOwner = "application"
	ownerMemory      packageOwner = "memory"
)

var excludedTestSupportDirectories = []string{
	"internal/harness/application/enginescenariotest",
	"internal/harness/application/eventstoretest",
	"internal/harness/engine/modeltest",
}

func shouldInspectProductionDirectory(directory string) bool {
	directory = filepath.ToSlash(filepath.Clean(directory))
	for _, excluded := range excludedTestSupportDirectories {
		if directoryWithin(directory, excluded) {
			return false
		}
	}
	return directoryWithin(directory, "internal/harness")
}

func packageOwnership(directory string) (packageOwner, bool) {
	directory = filepath.ToSlash(filepath.Clean(directory))
	for _, candidate := range []struct {
		root  string
		owner packageOwner
	}{
		{root: "internal/harness/domain", owner: ownerDomain},
		{root: "internal/harness/engine", owner: ownerEngine},
		{root: "internal/harness/application", owner: ownerApplication},
		{root: "internal/harness/adapters/memory", owner: ownerMemory},
	} {
		if directoryWithin(directory, candidate.root) {
			return candidate.owner, true
		}
	}
	return "", false
}

func directoryWithin(directory, root string) bool {
	return directory == root || strings.HasPrefix(directory, root+"/")
}

func forbiddenImport(owner packageOwner, importPath string) string {

	forbidden := make([]string, 0, 8)
	switch owner {
	case ownerDomain:
		forbidden = append(forbidden,
			modulePath+"/internal/harness/application",
			modulePath+"/internal/harness/engine",
			modulePath+"/internal/harness/adapters",
			modulePath+"/internal/harness/testkit",
		)
	case ownerEngine:
		forbidden = append(forbidden,
			modulePath+"/internal/harness/application",
			modulePath+"/internal/harness/adapters",
			modulePath+"/internal/harness/testkit",
		)
	case ownerApplication:
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
	if owner == ownerDomain || owner == ownerEngine {
		for _, segment := range []string{"acp", "mcp", "tui", "provider", "providers"} {
			if hasPathSegment(importPath, segment) {
				return "forbidden protocol/provider dependency"
			}
		}
	}
	if owner == ownerApplication || owner == ownerEngine || owner == ownerMemory {
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
