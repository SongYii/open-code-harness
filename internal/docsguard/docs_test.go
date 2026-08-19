// Package docsguard_test turns documentation rules that were prose into
// executable gates.
//
// The repository already enforces its architecture rules as a test. Its
// documentation rules were enforced by attention alone, and drifted: the root
// README described three landed slices as unimplemented because no gate
// required the two files to agree. These tests close that class.
//
// They check structure and reachability, never prose quality. A rule that
// cannot be checked without judgement stays prose and stays in
// docs/README.md.
package docsguard_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// repoRoot resolves the module root from this test's own location so the
// gates do not depend on the working directory a runner chooses.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root %s has no go.mod: %v", root, err)
	}
	return root
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// markdownFiles lists every tracked Markdown document the gates apply to.
func markdownFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	for _, name := range []string{"README.md", "SECURITY.md"} {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err == nil {
			files = append(files, path)
		}
	}
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs: %v", err)
	}
	sort.Strings(files)
	return files
}

// linkPattern matches inline Markdown links. Anchors, absolute URLs, and mail
// links are filtered by the callers, not by the pattern.
var linkPattern = regexp.MustCompile(`\]\(([^)\s]+)\)`)

func relativeLinks(document string) []string {
	var targets []string
	for _, match := range linkPattern.FindAllStringSubmatch(document, -1) {
		target := match[1]
		switch {
		case strings.HasPrefix(target, "http://"), strings.HasPrefix(target, "https://"):
		case strings.HasPrefix(target, "#"), strings.HasPrefix(target, "mailto:"):
		default:
			targets = append(targets, target)
		}
	}
	return targets
}

// TestRelativeLinksResolve is the cheapest gate and the one that already
// found a real break: a design document referenced the charter by
// repository-root path from a file that sits beside it.
func TestRelativeLinksResolve(t *testing.T) {
	root := repoRoot(t)
	for _, document := range markdownFiles(t, root) {
		for _, target := range relativeLinks(read(t, document)) {
			path := strings.SplitN(target, "#", 2)[0]
			if path == "" {
				continue
			}
			resolved := filepath.Join(filepath.Dir(document), path)
			if _, err := os.Stat(resolved); err != nil {
				rel, _ := filepath.Rel(root, document)
				t.Errorf("%s links to %q, which does not resolve to a file", rel, target)
			}
		}
	}
}

// authorityRow is one row of the authority table in docs/README.md.
type authorityRow struct {
	status    string
	authority string
	title     string
	target    string // repository-relative path
}

// authorityRows parses the authority table. A row is recognized by having the
// expected column count and a link in the document column; the table's
// surrounding prose is ignored.
func authorityRows(t *testing.T, root string) []authorityRow {
	t.Helper()
	var rows []authorityRow
	for _, line := range strings.Split(read(t, filepath.Join(root, "docs", "README.md")), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		columns := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(columns) != 4 {
			continue
		}
		document := strings.TrimSpace(columns[2])
		match := regexp.MustCompile(`\[(.*)\]\(([^)\s]+)\)`).FindStringSubmatch(document)
		if match == nil {
			continue
		}
		rows = append(rows, authorityRow{
			status:    strings.TrimSpace(columns[0]),
			authority: strings.TrimSpace(columns[1]),
			title:     match[1],
			target:    filepath.Join("docs", match[2]),
		})
	}
	if len(rows) < 10 {
		t.Fatalf("parsed %d authority rows; the table shape changed and this gate is no longer reading it", len(rows))
	}
	return rows
}

// TestAuthorityTableTargetsExist keeps the authority map from pointing at
// documents that were renamed or never written.
func TestAuthorityTableTargetsExist(t *testing.T) {
	root := repoRoot(t)
	for _, row := range authorityRows(t, root) {
		if _, err := os.Stat(filepath.Join(root, row.target)); err != nil {
			t.Errorf("authority row %q targets %s, which does not exist", row.title, row.target)
		}
	}
}

// TestImplementedContractsAppearInRootReadme is the drift gate.
//
// Documentation rule: an implemented contract is the project's claim about
// what the code already does. The root README is where a reader looks first.
// When a slice lands and only docs/README.md is updated, the front door
// understates the project — which is what happened for three consecutive
// slices before this gate existed.
func TestImplementedContractsAppearInRootReadme(t *testing.T) {
	root := repoRoot(t)
	readme := read(t, filepath.Join(root, "README.md"))
	for _, row := range authorityRows(t, root) {
		if row.status != "Implemented" || row.authority != "Implemented contract" {
			continue
		}
		// The README links relative to the repository root.
		if !strings.Contains(readme, row.target) {
			t.Errorf("README.md does not reference %s (%q), which docs/README.md lists as an implemented contract",
				row.target, row.title)
		}
	}
}

// TestReadingCopiesHaveANormativeSource keeps a translation from outliving
// the document it translates.
func TestReadingCopiesHaveANormativeSource(t *testing.T) {
	root := repoRoot(t)
	for _, document := range markdownFiles(t, root) {
		if !strings.HasSuffix(document, ".zh-CN.md") {
			continue
		}
		source := strings.TrimSuffix(document, ".zh-CN.md") + ".md"
		if _, err := os.Stat(source); err != nil {
			rel, _ := filepath.Rel(root, document)
			t.Errorf("%s has no English source beside it; a reading copy may not be the only authority", rel)
		}
	}
}

// TestReadingCopiesNameTheirNormativeSource makes the precedence rule
// visible in the file itself. docs/README.md states that the named normative
// source wins when copies diverge; a reader who opens only the translation
// must be able to learn that without consulting the authority map.
func TestReadingCopiesNameTheirNormativeSource(t *testing.T) {
	root := repoRoot(t)
	for _, document := range markdownFiles(t, root) {
		if !strings.HasSuffix(document, ".zh-CN.md") {
			continue
		}
		base := filepath.Base(strings.TrimSuffix(document, ".zh-CN.md") + ".md")
		if strings.Contains(read(t, document), base) {
			continue
		}
		rel, _ := filepath.Rel(root, document)
		t.Errorf("%s never names its normative source %s; a reading copy must say which document wins", rel, base)
	}
}

// TestEveryImplementedContractHasEvidence enforces the rule that a completion
// claim is backed by an auditable ledger rather than by prose or checkbox
// state.
func TestEveryImplementedContractHasEvidence(t *testing.T) {
	root := repoRoot(t)
	rows := authorityRows(t, root)
	ledgers := map[string]bool{}
	for _, row := range rows {
		if row.authority == "Evidence ledger" {
			ledgers[row.target] = true
		}
	}
	if len(ledgers) == 0 {
		t.Fatal("no evidence ledgers found; this gate is no longer reading the table")
	}
	// docs/architecture/domain-events.md documents milestone 1, which landed
	// before the evidence-ledger convention existed. The exemption is named
	// here rather than left implicit so that removing it is a deliberate act
	// and so that no later contract can inherit it by accident.
	exempt := map[string]string{
		"docs/architecture/domain-events.md": "milestone 1 predates the evidence-ledger convention",
	}
	for _, row := range rows {
		if row.status != "Implemented" || row.authority != "Implemented contract" {
			continue
		}
		if _, ok := exempt[row.target]; ok {
			continue
		}
		stem := strings.TrimSuffix(row.target, ".md")
		if ledgers[stem+"-evidence.md"] {
			continue
		}
		t.Errorf("implemented contract %s has no %s-evidence.md ledger row; a completion claim needs an auditable ledger",
			row.target, filepath.Base(stem))
	}
}

// TestDocumentationRulesStateWhichAreExecutable keeps this file discoverable.
// A gate nobody knows about is a gate contributors work around.
func TestDocumentationRulesStateWhichAreExecutable(t *testing.T) {
	root := repoRoot(t)
	rules := read(t, filepath.Join(root, "docs", "README.md"))
	const marker = "internal/docsguard"
	if !strings.Contains(rules, marker) {
		t.Fatalf("docs/README.md does not mention %s; executable rules must be discoverable from the rules section", marker)
	}
}

func init() {
	// Fail loudly rather than silently passing if the layout assumption breaks.
	if _, err := os.Stat("docs_test.go"); err != nil {
		panic(fmt.Sprintf("docsguard gates must run from their own directory: %v", err))
	}
}
