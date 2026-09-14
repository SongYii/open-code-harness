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

// Check references, not just direct calls: saving a planner in a function
// variable must not evade the single application planning boundary.
func planningBoundaryViolations(relative string, file *ast.File) []string {
	var violations []string
	aliases := map[string]bool{}
	for _, spec := range file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		if path != modulePath+"/internal/harness/contextengine" {
			continue
		}
		name := "contextengine"
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "." {
			violations = append(violations, "dot import conceals context planning references")
		}
		aliases[name] = true
	}
	for _, decl := range file.Decls {
		bridge := false
		var declaredName *ast.Ident
		if fn, ok := decl.(*ast.FuncDecl); ok {
			declaredName = fn.Name
			bridge = relative == "internal/harness/application/context_policy.go" && fn.Recv == nil && fn.Name.Name == "planContext"
		}
		ast.Inspect(decl, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok {
				if ident, ok := selector.X.(*ast.Ident); ok && aliases[ident.Name] {
					switch selector.Sel.Name {
					case "SelectCutPoint":
						violations = append(violations, "SelectCutPoint must remain behind contextengine.Plan")
					case "Plan":
						if !bridge {
							violations = append(violations, "contextengine.Plan must be reached through application.planContext")
						}
					}
				}
			}
			if ident, ok := node.(*ast.Ident); ok && ident.Name == "SelectCutPoint" && ident != declaredName &&
				filepath.ToSlash(filepath.Dir(relative)) == "internal/harness/contextengine" && relative != "internal/harness/contextengine/policy.go" {
				violations = append(violations, "core selector reference outside the policy gateway")
			}
			return true
		})
	}
	return violations
}

func TestProductionContextPlanningUsesSingleGateway(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	for _, directory := range []string{"internal", "cmd", "sdk"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if !shouldInspectProductionDirectory(filepath.ToSlash(filepath.Dir(relative))) {
				return nil
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			for _, violation := range planningBoundaryViolations(relative, parsed) {
				t.Errorf("%s: %s", relative, violation)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlanningBoundaryGuardDetectsBypasses(t *testing.T) {
	const imported = `package application; import ce "github.com/SongYii/open-code-harness/internal/harness/contextengine"; `
	for _, test := range []struct {
		name, path, source string
		allowed            bool
	}{
		{"bridge", "application/context_policy.go", imported + `func planContext() { ce.Plan() }`, true},
		{"manual bypass", "application/context_manual.go", imported + `func compact() { ce.SelectCutPoint() }`, false},
		{"selector value", "application/context_manual.go", imported + `var cut = ce.SelectCutPoint`, false},
		{"plan bypass", "application/context_manual.go", imported + `func compact() { ce.Plan() }`, false},
		{"plan value", "application/context_manual.go", imported + `var plan = ce.Plan`, false},
		{"wrong function in bridge file", "application/context_policy.go", imported + `func bypass() { ce.Plan() }`, false},
		{"wrong receiver in bridge file", "application/context_policy.go", imported + `func (s Service) planContext() { ce.Plan() }`, false},
		{"selector in bridge", "application/context_policy.go", imported + `func planContext() { ce.SelectCutPoint() }`, false},
		{"dot import", "application/context_manual.go", `package application; import . "github.com/SongYii/open-code-harness/internal/harness/contextengine"; var cut = SelectCutPoint`, false},
		{"unrelated selector", "application/context_manual.go", `package application; func f() { other.Plan() }`, true},
		{"core gateway", "contextengine/policy.go", `package contextengine; func Plan() { SelectCutPoint() }`, true},
		{"core selector definition", "contextengine/planner.go", `package contextengine; func SelectCutPoint() {}`, true},
		{"core bypass", "contextengine/other.go", `package contextengine; var cut = SelectCutPoint`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), test.path, test.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			violations := planningBoundaryViolations("internal/harness/"+test.path, file)
			if (len(violations) == 0) != test.allowed {
				t.Fatalf("allowed=%t, violations=%v", test.allowed, violations)
			}
		})
	}
}
