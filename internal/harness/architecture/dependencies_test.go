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
		{name: "openaicompat root", directory: "internal/harness/adapters/openaicompat", want: ownerOpenAICompat, inspect: true, hasOwner: true},
		{name: "openaicompat production subpackage", directory: "internal/harness/adapters/openaicompat/sse", want: ownerOpenAICompat, inspect: true, hasOwner: true},
		{name: "policy root", directory: "internal/harness/policy", want: ownerPolicy, inspect: true, hasOwner: true},
		{name: "policy production subpackage", directory: "internal/harness/policy/rules", want: ownerPolicy, inspect: true, hasOwner: true},
		{name: "tools root", directory: "internal/harness/tools", want: ownerTools, inspect: true, hasOwner: true},
		{name: "tools production subpackage", directory: "internal/harness/tools/catalog", want: ownerTools, inspect: true, hasOwner: true},
		{name: "tools port test support", directory: "internal/harness/tools/porttest", want: ownerTools, inspect: false, hasOwner: true},
		{name: "tools port nested test support", directory: "internal/harness/tools/porttest/internal", want: ownerTools, inspect: false, hasOwner: true},
		{name: "similarly named tools port child", directory: "internal/harness/tools/porttestkit", want: ownerTools, inspect: true, hasOwner: true},
		{name: "workspacefs root", directory: "internal/harness/adapters/workspacefs", want: ownerWorkspaceFS, inspect: true, hasOwner: true},
		{name: "workspacefs production subpackage", directory: "internal/harness/adapters/workspacefs/internal", want: ownerWorkspaceFS, inspect: true, hasOwner: true},
		{name: "localexec root", directory: "internal/harness/adapters/localexec", want: ownerLocalExec, inspect: true, hasOwner: true},
		{name: "localexec production subpackage", directory: "internal/harness/adapters/localexec/internal", want: ownerLocalExec, inspect: true, hasOwner: true},
		{name: "sqlite root", directory: "internal/harness/adapters/sqlite", want: ownerSQLite, inspect: true, hasOwner: true},
		{name: "runtime root", directory: "internal/harness/runtime", want: ownerRuntime, inspect: true, hasOwner: true},
		{name: "runtime production subpackage", directory: "internal/harness/runtime/internal", want: ownerRuntime, inspect: true, hasOwner: true},
		{name: "sqlite production subpackage", directory: "internal/harness/adapters/sqlite/internal", want: ownerSQLite, inspect: true, hasOwner: true},
		{name: "scenario test support", directory: "internal/harness/application/enginescenariotest", want: ownerApplication, inspect: false, hasOwner: true},
		{name: "scenario nested test support", directory: "internal/harness/application/enginescenariotest/internal", want: ownerApplication, inspect: false, hasOwner: true},
		{name: "similarly named production child", directory: "internal/harness/application/enginescenariotestkit", want: ownerApplication, inspect: true, hasOwner: true},
		{name: "event store test support", directory: "internal/harness/application/eventstoretest", want: ownerApplication, inspect: false, hasOwner: true},
		{name: "model test support", directory: "internal/harness/engine/modeltest", want: ownerEngine, inspect: false, hasOwner: true},
		{name: "system root", directory: "internal/harness/adapters/system", want: ownerSystem, inspect: true, hasOwner: true},
		{name: "composition root", directory: "internal/harness/composition", want: ownerComposition, inspect: true, hasOwner: true},
		{name: "composition production subpackage", directory: "internal/harness/composition/wiring", want: ownerComposition, inspect: true, hasOwner: true},
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

func TestAllowAllConstructorDeclKeyedOnPolicyPath(t *testing.T) {
	source := "package policy\nfunc AllowAll() {}\n"
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "allowall.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var ident *ast.Ident
	for _, decl := range parsed.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name != nil && fn.Name.Name == "AllowAll" {
			ident = fn.Name
			break
		}
	}
	if ident == nil {
		t.Fatal("fixture missing func AllowAll")
	}
	tests := []struct {
		name     string
		relative string
		allowed  bool
	}{
		{name: "policy root constructor", relative: "internal/harness/policy/engine.go", allowed: true},
		{name: "policy nested constructor", relative: "internal/harness/policy/rules/allowall.go", allowed: true},
		{name: "unowned package policy", relative: "internal/harness/adapters/other/allowall.go"},
		{name: "application package policy", relative: "internal/harness/application/service.go"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := allowAllConstructorDecl(test.relative, parsed, ident); got != test.allowed {
				t.Fatalf("allowAllConstructorDecl(%q) = %t, want %t", test.relative, got, test.allowed)
			}
		})
	}
}

func TestAllowAllProductionException(t *testing.T) {
	tests := []struct {
		name      string
		relative  string
		exception bool
	}{
		{name: "testkit root", relative: "internal/harness/testkit/policy.go", exception: true},
		{name: "testkit nested", relative: "internal/harness/testkit/internal/policy.go", exception: true},
		{name: "application production", relative: "internal/harness/application/service.go"},
		{name: "policy production use", relative: "internal/harness/policy/engine.go"},
		{name: "adapters production", relative: "internal/harness/adapters/memory/event_store.go"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := allowAllProductionException(test.relative); got != test.exception {
				t.Fatalf("allowAllProductionException(%q) = %t, want %t", test.relative, got, test.exception)
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
		{name: "domain cannot import net/http", owner: ownerDomain, importPath: "net/http", forbidden: true},
		{name: "engine cannot import net/http", owner: ownerEngine, importPath: "net/http", forbidden: true},
		{name: "application cannot import net/http", owner: ownerApplication, importPath: "net/http", forbidden: true},
		{name: "application cannot import openaicompat", owner: ownerApplication, importPath: modulePath + "/internal/harness/adapters/openaicompat", forbidden: true},
		{name: "composition may import sqlite", owner: ownerComposition, importPath: modulePath + "/internal/harness/adapters/sqlite", forbidden: false},
		{name: "composition may import openaicompat", owner: ownerComposition, importPath: modulePath + "/internal/harness/adapters/openaicompat", forbidden: false},
		{name: "composition may import localexec", owner: ownerComposition, importPath: modulePath + "/internal/harness/adapters/localexec", forbidden: false},
		{name: "composition may import runtime", owner: ownerComposition, importPath: modulePath + "/internal/harness/runtime", forbidden: false},
		{name: "composition cannot import testkit", owner: ownerComposition, importPath: modulePath + "/internal/harness/testkit", forbidden: true},
		{name: "system cannot import adapters", owner: ownerSystem, importPath: modulePath + "/internal/harness/adapters/sqlite", forbidden: true},
		{name: "system cannot import engine", owner: ownerSystem, importPath: modulePath + "/internal/harness/engine", forbidden: true},
		{name: "system may import application", owner: ownerSystem, importPath: modulePath + "/internal/harness/application", forbidden: false},
		{name: "openaicompat may import net/http", owner: ownerOpenAICompat, importPath: "net/http", forbidden: false},
		{name: "openaicompat may import os", owner: ownerOpenAICompat, importPath: "os", forbidden: false},
		{name: "openaicompat may import engine", owner: ownerOpenAICompat, importPath: modulePath + "/internal/harness/engine", forbidden: false},
		{name: "openaicompat cannot import os/exec", owner: ownerOpenAICompat, importPath: "os/exec", forbidden: true},
		{name: "openaicompat cannot import application", owner: ownerOpenAICompat, importPath: modulePath + "/internal/harness/application", forbidden: true},
		{name: "openaicompat cannot import testkit", owner: ownerOpenAICompat, importPath: modulePath + "/internal/harness/testkit", forbidden: true},
		{name: "openaicompat cannot import memory adapter", owner: ownerOpenAICompat, importPath: modulePath + "/internal/harness/adapters/memory", forbidden: true},
		{name: "openaicompat cannot import tools", owner: ownerOpenAICompat, importPath: modulePath + "/internal/harness/tools", forbidden: true},
		{name: "policy cannot import application", owner: ownerPolicy, importPath: modulePath + "/internal/harness/application", forbidden: true},
		{name: "policy cannot import engine", owner: ownerPolicy, importPath: modulePath + "/internal/harness/engine", forbidden: true},
		{name: "policy cannot import tools", owner: ownerPolicy, importPath: modulePath + "/internal/harness/tools", forbidden: true},
		{name: "policy cannot import adapters", owner: ownerPolicy, importPath: modulePath + "/internal/harness/adapters/memory", forbidden: true},
		{name: "policy cannot import testkit", owner: ownerPolicy, importPath: modulePath + "/internal/harness/testkit", forbidden: true},
		{name: "policy cannot import os", owner: ownerPolicy, importPath: "os", forbidden: true},
		{name: "policy cannot import os/exec", owner: ownerPolicy, importPath: "os/exec", forbidden: true},
		{name: "policy cannot import net", owner: ownerPolicy, importPath: "net", forbidden: true},
		{name: "policy cannot import net/http", owner: ownerPolicy, importPath: "net/http", forbidden: true},
		{name: "policy may import domain", owner: ownerPolicy, importPath: modulePath + "/internal/harness/domain", forbidden: false},
		{name: "policy may import strings", owner: ownerPolicy, importPath: "strings", forbidden: false},
		{name: "tools may import domain", owner: ownerTools, importPath: modulePath + "/internal/harness/domain", forbidden: false},
		{name: "tools may import path/filepath", owner: ownerTools, importPath: "path/filepath", forbidden: false},
		{name: "tools cannot import policy", owner: ownerTools, importPath: modulePath + "/internal/harness/policy", forbidden: true},
		{name: "tools cannot import application", owner: ownerTools, importPath: modulePath + "/internal/harness/application", forbidden: true},
		{name: "tools cannot import adapters", owner: ownerTools, importPath: modulePath + "/internal/harness/adapters/workspacefs", forbidden: true},
		{name: "tools cannot import testkit", owner: ownerTools, importPath: modulePath + "/internal/harness/testkit", forbidden: true},
		{name: "tools cannot import engine", owner: ownerTools, importPath: modulePath + "/internal/harness/engine", forbidden: true},
		{name: "tools cannot import os", owner: ownerTools, importPath: "os", forbidden: true},
		{name: "tools cannot import os/exec", owner: ownerTools, importPath: "os/exec", forbidden: true},
		{name: "tools cannot import net", owner: ownerTools, importPath: "net", forbidden: true},
		{name: "tools cannot import net/http", owner: ownerTools, importPath: "net/http", forbidden: true},
		{name: "domain cannot import tools", owner: ownerDomain, importPath: modulePath + "/internal/harness/tools", forbidden: true},
		{name: "workspacefs may import tools", owner: ownerWorkspaceFS, importPath: modulePath + "/internal/harness/tools", forbidden: false},
		{name: "workspacefs may import domain", owner: ownerWorkspaceFS, importPath: modulePath + "/internal/harness/domain", forbidden: false},
		{name: "workspacefs may import os", owner: ownerWorkspaceFS, importPath: "os", forbidden: false},
		{name: "workspacefs may import path/filepath", owner: ownerWorkspaceFS, importPath: "path/filepath", forbidden: false},
		{name: "workspacefs cannot import application", owner: ownerWorkspaceFS, importPath: modulePath + "/internal/harness/application", forbidden: true},
		{name: "workspacefs cannot import testkit", owner: ownerWorkspaceFS, importPath: modulePath + "/internal/harness/testkit", forbidden: true},
		{name: "workspacefs cannot import os/exec", owner: ownerWorkspaceFS, importPath: "os/exec", forbidden: true},
		{name: "workspacefs cannot import net", owner: ownerWorkspaceFS, importPath: "net", forbidden: true},
		{name: "workspacefs cannot import net/http", owner: ownerWorkspaceFS, importPath: "net/http", forbidden: true},
		{name: "workspacefs cannot import localexec", owner: ownerWorkspaceFS, importPath: modulePath + "/internal/harness/adapters/localexec", forbidden: true},
		{name: "workspacefs cannot import memory", owner: ownerWorkspaceFS, importPath: modulePath + "/internal/harness/adapters/memory", forbidden: true},
		{name: "workspacefs cannot import openaicompat", owner: ownerWorkspaceFS, importPath: modulePath + "/internal/harness/adapters/openaicompat", forbidden: true},
		{name: "workspacefs cannot import adapters root", owner: ownerWorkspaceFS, importPath: modulePath + "/internal/harness/adapters", forbidden: true},
		{name: "localexec may import tools", owner: ownerLocalExec, importPath: modulePath + "/internal/harness/tools", forbidden: false},
		{name: "localexec may import domain", owner: ownerLocalExec, importPath: modulePath + "/internal/harness/domain", forbidden: false},
		{name: "localexec may import os", owner: ownerLocalExec, importPath: "os", forbidden: false},
		{name: "localexec may import os/exec", owner: ownerLocalExec, importPath: "os/exec", forbidden: false},
		{name: "localexec cannot import application", owner: ownerLocalExec, importPath: modulePath + "/internal/harness/application", forbidden: true},
		{name: "localexec cannot import testkit", owner: ownerLocalExec, importPath: modulePath + "/internal/harness/testkit", forbidden: true},
		{name: "localexec cannot import net", owner: ownerLocalExec, importPath: "net", forbidden: true},
		{name: "localexec cannot import net/http", owner: ownerLocalExec, importPath: "net/http", forbidden: true},
		{name: "localexec cannot import workspacefs", owner: ownerLocalExec, importPath: modulePath + "/internal/harness/adapters/workspacefs", forbidden: true},
		{name: "localexec cannot import memory", owner: ownerLocalExec, importPath: modulePath + "/internal/harness/adapters/memory", forbidden: true},
		{name: "localexec cannot import openaicompat", owner: ownerLocalExec, importPath: modulePath + "/internal/harness/adapters/openaicompat", forbidden: true},
		{name: "localexec cannot import adapters root", owner: ownerLocalExec, importPath: modulePath + "/internal/harness/adapters", forbidden: true},
		{name: "memory cannot import workspacefs", owner: ownerMemory, importPath: modulePath + "/internal/harness/adapters/workspacefs", forbidden: true},
		{name: "memory cannot import localexec", owner: ownerMemory, importPath: modulePath + "/internal/harness/adapters/localexec", forbidden: true},
		{name: "openaicompat cannot import workspacefs", owner: ownerOpenAICompat, importPath: modulePath + "/internal/harness/adapters/workspacefs", forbidden: true},
		{name: "openaicompat cannot import localexec", owner: ownerOpenAICompat, importPath: modulePath + "/internal/harness/adapters/localexec", forbidden: true},
		{name: "sqlite may import application", owner: ownerSQLite, importPath: modulePath + "/internal/harness/application", forbidden: false},
		{name: "sqlite may import domain", owner: ownerSQLite, importPath: modulePath + "/internal/harness/domain", forbidden: false},
		{name: "sqlite cannot import engine", owner: ownerSQLite, importPath: modulePath + "/internal/harness/engine", forbidden: true},
		{name: "sqlite cannot import tools", owner: ownerSQLite, importPath: modulePath + "/internal/harness/tools", forbidden: true},
		{name: "sqlite cannot import policy", owner: ownerSQLite, importPath: modulePath + "/internal/harness/policy", forbidden: true},
		{name: "sqlite cannot import testkit", owner: ownerSQLite, importPath: modulePath + "/internal/harness/testkit", forbidden: true},
		{name: "sqlite cannot import memory", owner: ownerSQLite, importPath: modulePath + "/internal/harness/adapters/memory", forbidden: true},
		{name: "sqlite may import os for file-based export", owner: ownerSQLite, importPath: "os", forbidden: false},
		{name: "runtime may import application", owner: ownerRuntime, importPath: modulePath + "/internal/harness/application", forbidden: false},
		{name: "runtime may import domain", owner: ownerRuntime, importPath: modulePath + "/internal/harness/domain", forbidden: false},
		{name: "runtime may import sqlite adapter", owner: ownerRuntime, importPath: modulePath + "/internal/harness/adapters/sqlite", forbidden: false},
		{name: "runtime cannot import engine", owner: ownerRuntime, importPath: modulePath + "/internal/harness/engine", forbidden: true},
		{name: "runtime cannot import tools", owner: ownerRuntime, importPath: modulePath + "/internal/harness/tools", forbidden: true},
		{name: "runtime cannot import policy", owner: ownerRuntime, importPath: modulePath + "/internal/harness/policy", forbidden: true},
		{name: "runtime cannot import testkit", owner: ownerRuntime, importPath: modulePath + "/internal/harness/testkit", forbidden: true},
		{name: "runtime cannot import memory adapter", owner: ownerRuntime, importPath: modulePath + "/internal/harness/adapters/memory", forbidden: true},
		{name: "runtime cannot import openaicompat", owner: ownerRuntime, importPath: modulePath + "/internal/harness/adapters/openaicompat", forbidden: true},
		{name: "sqlite cannot import os/exec", owner: ownerSQLite, importPath: "os/exec", forbidden: true},
		{name: "sqlite cannot import net", owner: ownerSQLite, importPath: "net", forbidden: true},
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
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			// A directory with no declared owner is checked against the
			// unowned rules, not skipped. Skipping would make "no owner"
			// mean "unrestricted", so a new package could inherit the
			// composition exception simply by not being listed.
			reason := unownedImport(importPath)
			if hasOwner {
				reason = forbiddenImport(owner, importPath)
			}
			if reason != "" {
				position := fileSet.Position(spec.Pos())
				violations = append(violations, position.String()+": "+reason+" "+strconv.Quote(importPath))
			}
		}
		appendAllowAllViolations(fileSet, path, relative, parsed, &violations)
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
	ownerDomain       packageOwner = "domain"
	ownerEngine       packageOwner = "engine"
	ownerApplication  packageOwner = "application"
	ownerMemory       packageOwner = "memory"
	ownerOpenAICompat packageOwner = "openaicompat"
	ownerSQLite       packageOwner = "sqlite"
	ownerRuntime      packageOwner = "runtime"
	ownerPolicy       packageOwner = "policy"
	ownerTools        packageOwner = "tools"
	ownerWorkspaceFS  packageOwner = "workspacefs"
	ownerLocalExec    packageOwner = "localexec"
	ownerSystem       packageOwner = "system"
	ownerComposition  packageOwner = "composition"
)

var excludedTestSupportDirectories = []string{
	"internal/harness/application/enginescenariotest",
	"internal/harness/application/eventstoretest",
	"internal/harness/engine/modeltest",
	"internal/harness/tools/porttest",
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
		{root: "internal/harness/adapters/openaicompat", owner: ownerOpenAICompat},
		{root: "internal/harness/adapters/sqlite", owner: ownerSQLite},
		{root: "internal/harness/runtime", owner: ownerRuntime},
		{root: "internal/harness/policy", owner: ownerPolicy},
		{root: "internal/harness/adapters/workspacefs", owner: ownerWorkspaceFS},
		{root: "internal/harness/adapters/localexec", owner: ownerLocalExec},
		{root: "internal/harness/adapters/system", owner: ownerSystem},
		{root: "internal/harness/composition", owner: ownerComposition},
		{root: "internal/harness/tools", owner: ownerTools},
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

// unownedImport applies to a production directory under internal/harness
// that no owner claims. Only the composition root may name an adapter, and
// only a test may name testkit; a package nobody has classified may do
// neither. Adding a package therefore requires either staying inside those
// bounds or declaring an owner deliberately.
func withinPackage(importPath, prefix string) bool {
	return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
}

func unownedImport(importPath string) string {
	for _, prefix := range []string{
		modulePath + "/internal/harness/adapters",
		modulePath + "/internal/harness/testkit",
	} {
		if withinPackage(importPath, prefix) {
			return "forbidden package dependency from an unowned package"
		}
	}
	return ""
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
			modulePath+"/internal/harness/tools",
			modulePath+"/internal/harness/policy",
		)
	case ownerEngine:
		forbidden = append(forbidden,
			modulePath+"/internal/harness/application",
			modulePath+"/internal/harness/adapters",
			modulePath+"/internal/harness/testkit",
			modulePath+"/internal/harness/tools",
			modulePath+"/internal/harness/policy",
		)
	case ownerTools:
		forbidden = append(forbidden,
			modulePath+"/internal/harness/policy",
			modulePath+"/internal/harness/application",
			modulePath+"/internal/harness/adapters",
			modulePath+"/internal/harness/testkit",
			modulePath+"/internal/harness/engine",
		)
	case ownerApplication:
		forbidden = append(forbidden,
			modulePath+"/internal/harness/adapters",
			modulePath+"/internal/harness/testkit",
		)
	case ownerOpenAICompat:
		forbidden = append(forbidden,
			modulePath+"/internal/harness/application",
			modulePath+"/internal/harness/testkit",
			modulePath+"/internal/harness/tools",
		)
	case ownerPolicy:
		forbidden = append(forbidden,
			modulePath+"/internal/harness/application",
			modulePath+"/internal/harness/engine",
			modulePath+"/internal/harness/tools",
			modulePath+"/internal/harness/adapters",
			modulePath+"/internal/harness/testkit",
		)
	case ownerSQLite:
		forbidden = append(forbidden,
			modulePath+"/internal/harness/engine",
			modulePath+"/internal/harness/tools",
			modulePath+"/internal/harness/policy",
			modulePath+"/internal/harness/testkit",
		)
	case ownerRuntime:
		// Runtime is denied the adapters root as a whole, with sqlite carved
		// out below. Enumerating the denied adapters instead would mean every
		// adapter added later silently widened Runtime's reach, which is how
		// adapters/system first became importable here.
		forbidden = append(forbidden,
			modulePath+"/internal/harness/engine",
			modulePath+"/internal/harness/tools",
			modulePath+"/internal/harness/policy",
			modulePath+"/internal/harness/testkit",
			modulePath+"/internal/harness/adapters",
		)
	case ownerSystem:
		forbidden = append(forbidden,
			modulePath+"/internal/harness/engine",
			modulePath+"/internal/harness/tools",
			modulePath+"/internal/harness/policy",
			modulePath+"/internal/harness/runtime",
			modulePath+"/internal/harness/testkit",
			modulePath+"/internal/harness/adapters",
		)
	case ownerComposition:
		// The composition root may name every adapter. That is the whole
		// point of the package, and the reason no other package may.
		// Test support stays forbidden: production wiring must not reach
		// for a double.
		forbidden = append(forbidden,
			modulePath+"/internal/harness/testkit",
		)
	case ownerWorkspaceFS, ownerLocalExec:
		forbidden = append(forbidden,
			modulePath+"/internal/harness/application",
			modulePath+"/internal/harness/testkit",
			modulePath+"/internal/harness/policy",
			modulePath+"/internal/harness/engine",
		)
	}
	// The Runtime Host owns the canonical store's lifecycle and its Config
	// embeds sqlite.Config; the Slice 4 design established that dependency.
	// It is the only adapter Runtime may name.
	runtimeSQLite := ownerRuntime == owner && withinPackage(importPath, modulePath+"/internal/harness/adapters/sqlite")
	for _, prefix := range forbidden {
		if runtimeSQLite {
			break
		}
		if withinPackage(importPath, prefix) {
			return "forbidden package dependency"
		}
	}
	if selfRoot, ok := adapterOwnerRoot(owner); ok && otherAdapterImport(importPath, selfRoot) {
		return "forbidden package dependency"
	}
	if owner == ownerDomain || owner == ownerEngine {
		for _, segment := range []string{"acp", "mcp", "tui", "provider", "providers"} {
			if hasPathSegment(importPath, segment) {
				return "forbidden protocol/provider dependency"
			}
		}
	}
	if owner == ownerDomain || owner == ownerApplication || owner == ownerEngine || owner == ownerMemory || owner == ownerPolicy || owner == ownerTools {
		switch importPath {
		case "os", "os/exec", "net", "net/http":
			return "forbidden host/network dependency"
		}
	}
	if owner == ownerOpenAICompat && importPath == "os/exec" {
		return "forbidden host/network dependency"
	}
	if owner == ownerSQLite {
		switch importPath {
		case "net", "net/http", "os/exec":
			return "forbidden host/network dependency"
		}
	}
	if owner == ownerWorkspaceFS {
		switch importPath {
		case "os/exec", "net", "net/http":
			return "forbidden host/network dependency"
		}
	}
	if owner == ownerLocalExec {
		switch importPath {
		case "net", "net/http":
			return "forbidden host/network dependency"
		}
	}
	return ""
}

func adapterOwnerRoot(owner packageOwner) (string, bool) {
	adaptersRoot := modulePath + "/internal/harness/adapters"
	switch owner {
	case ownerMemory:
		return adaptersRoot + "/memory", true
	case ownerOpenAICompat:
		return adaptersRoot + "/openaicompat", true
	case ownerWorkspaceFS:
		return adaptersRoot + "/workspacefs", true
	case ownerLocalExec:
		return adaptersRoot + "/localexec", true
	case ownerSQLite:
		return adaptersRoot + "/sqlite", true
	default:
		return "", false
	}
}

func otherAdapterImport(importPath, selfRoot string) bool {
	adaptersRoot := modulePath + "/internal/harness/adapters"
	if importPath == adaptersRoot {
		return true
	}
	if !strings.HasPrefix(importPath, adaptersRoot+"/") {
		return false
	}
	return !directoryWithin(importPath, selfRoot)
}

func hasPathSegment(path, wanted string) bool {
	for _, segment := range strings.Split(path, "/") {
		if strings.EqualFold(segment, wanted) {
			return true
		}
	}
	return false
}

func TestOsExecOnlyInLocalExec(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate dependency test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", ".."))
	harnessRoot := filepath.Join(repositoryRoot, "internal", "harness")
	fileSet := token.NewFileSet()
	var violations []string
	localExecRoot := filepath.Join(harnessRoot, "adapters", "localexec")

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
		parsed, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			return err
		}
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if importPath != "os/exec" {
				continue
			}
			if !directoryWithin(filepath.ToSlash(filepath.Dir(path)), filepath.ToSlash(localExecRoot)) {
				position := fileSet.Position(spec.Pos())
				violations = append(violations, position.String()+": os/exec is only allowed in adapters/localexec")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("os/exec import violations:\n%s", strings.Join(violations, "\n"))
	}
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

func appendAllowAllViolations(fileSet *token.FileSet, path, relative string, parsed *ast.File, violations *[]string) {
	if allowAllProductionException(relative) {
		return
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok || ident.Name != "AllowAll" {
			return true
		}
		if allowAllConstructorDecl(relative, parsed, ident) {
			return true
		}
		position := fileSet.Position(ident.Pos())
		if position.Filename == "" {
			position.Filename = path
		}
		*violations = append(*violations, position.String()+": AllowAll is test-only")
		return true
	})
}

func allowAllProductionException(relative string) bool {
	directory := filepath.ToSlash(filepath.Dir(relative))
	return directoryWithin(directory, "internal/harness/testkit")
}

func allowAllConstructorDecl(relative string, parsed *ast.File, ident *ast.Ident) bool {
	directory := filepath.ToSlash(filepath.Dir(relative))
	if !directoryWithin(directory, "internal/harness/policy") {
		return false
	}
	if parsed == nil {
		return false
	}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name == ident && fn.Recv == nil {
			return true
		}
	}
	return false
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

// TestUnownedPackagesCannotImportAdapters pins the property that makes the
// composition exception safe: adding a package under internal/harness without
// declaring an owner does not grant it the right to name an adapter.
//
// Before this rule the walk skipped unowned directories entirely, so "no
// owner" meant "unrestricted" rather than "forbidden".
func TestUnownedPackagesCannotImportAdapters(t *testing.T) {
	tests := []struct {
		name       string
		importPath string
		forbidden  bool
	}{
		{name: "adapter root", importPath: modulePath + "/internal/harness/adapters/sqlite", forbidden: true},
		{name: "adapter subpackage", importPath: modulePath + "/internal/harness/adapters/sqlite/internal", forbidden: true},
		{name: "adapters parent", importPath: modulePath + "/internal/harness/adapters", forbidden: true},
		{name: "test support", importPath: modulePath + "/internal/harness/testkit", forbidden: true},
		{name: "similarly named package is not an adapter", importPath: modulePath + "/internal/harness/adaptersx", forbidden: false},
		{name: "domain stays permitted", importPath: modulePath + "/internal/harness/domain", forbidden: false},
		{name: "standard library stays permitted", importPath: "time", forbidden: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := unownedImport(test.importPath) != ""; got != test.forbidden {
				t.Fatalf("unownedImport(%q) forbidden = %t, want %t", test.importPath, got, test.forbidden)
			}
		})
	}
}

// TestOnlyCompositionAndRuntimeMayNameAnAdapter states the exception as an
// exhaustive claim rather than a comment, so widening it fails here.
//
// Two owners may name an adapter, and the second is narrow. The composition
// root may name every adapter, which is the whole purpose of the package.
// Runtime may name sqlite and nothing else: the Runtime Host owns the
// canonical store's lifecycle and its Config embeds sqlite.Config, a
// dependency the Slice 4 design established. An adapter may of course name
// itself. Every other pairing is forbidden.
func TestOnlyCompositionAndRuntimeMayNameAnAdapter(t *testing.T) {
	adapters := []string{
		modulePath + "/internal/harness/adapters/sqlite",
		modulePath + "/internal/harness/adapters/openaicompat",
		modulePath + "/internal/harness/adapters/memory",
		modulePath + "/internal/harness/adapters/workspacefs",
		modulePath + "/internal/harness/adapters/localexec",
		modulePath + "/internal/harness/adapters/system",
	}
	owners := []packageOwner{
		ownerDomain, ownerEngine, ownerApplication, ownerPolicy, ownerTools,
		ownerRuntime, ownerMemory, ownerOpenAICompat, ownerSQLite,
		ownerWorkspaceFS, ownerLocalExec, ownerSystem,
	}
	permitted := func(owner packageOwner, adapter string) bool {
		if selfRoot, ok := adapterOwnerRoot(owner); ok && adapter == selfRoot {
			return true
		}
		return owner == ownerRuntime && adapter == modulePath+"/internal/harness/adapters/sqlite"
	}
	for _, owner := range owners {
		for _, adapter := range adapters {
			allowed := forbiddenImport(owner, adapter) == ""
			if allowed != permitted(owner, adapter) {
				t.Errorf("owner %q import of %s: allowed=%t, want %t", owner, adapter, allowed, permitted(owner, adapter))
			}
		}
	}
	for _, adapter := range adapters {
		if reason := forbiddenImport(ownerComposition, adapter); reason != "" {
			t.Errorf("composition may not import %s (%s); the root must be able to name every adapter", adapter, reason)
		}
	}
}
