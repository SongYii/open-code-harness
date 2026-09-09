//go:build unix

// The wiring guard for the scheduled Context lane.
//
// Every assertion here checks an executable fact — what the test binary does
// when the opt-in is absent, and what `.github/workflows/ci.yml` actually
// tells the runner to do. None of it reads a comment or a design document.
// That distinction is the whole point: between 2026-09-04's two commits the
// lane's own comment, its commit message, and the Evaluation contract all
// said the full matrix "never" ran in ordinary PR CI while the only gate in
// the code was testing.Short(), which no CI job passes. Prose that describes
// an execution boundary cannot enforce one.

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

// scheduledContextMatrixEnv opts a run in to the full Context mechanism
// matrix: nine EvalSets, five of which drive real `och -acp` subprocesses.
// Measured on the development machine at 39s without the race detector and
// 64s with it, so the pull-request path would pay it four times over — once
// in the `go` job and three more under `determinism` — for a signal neither
// job is asking for. An end-to-end subprocess matrix is not a flakiness
// sample.
//
// It follows the DOCSGUARD_CHECK_EXTERNAL_LINKS precedent in
// internal/docsguard/citations_test.go: a lane too expensive or too
// environment-dependent for every pull request runs only where it is asked
// for, by name.
const scheduledContextMatrixEnv = "OCH_EVAL_SCHEDULED_CONTEXT_MATRIX"

// scheduledContextMatrixEnabled reports whether a raw environment value opts
// in. Only "1" does. An empty, unset, or any other value stays off, so a
// half-set variable fails closed rather than silently enabling the matrix.
func scheduledContextMatrixEnabled(value string) bool {
	return value == "1"
}

// requireScheduledContextMatrix skips unless the opt-in is present. This is
// the gate the lane's exclusion from ordinary CI actually rests on.
func requireScheduledContextMatrix(t *testing.T) {
	t.Helper()
	if !scheduledContextMatrixEnabled(os.Getenv(scheduledContextMatrixEnv)) {
		t.Skipf("full Context matrix runs only when %s=1", scheduledContextMatrixEnv)
	}
}

func TestScheduledContextMatrixOptInFailsClosed(t *testing.T) {
	for _, value := range []string{"", "0", "true", "yes", "2", " 1"} {
		if scheduledContextMatrixEnabled(value) {
			t.Errorf("%s=%q enabled the full matrix; only \"1\" may", scheduledContextMatrixEnv, value)
		}
	}
	if !scheduledContextMatrixEnabled("1") {
		t.Errorf("%s=1 did not enable the full matrix", scheduledContextMatrixEnv)
	}
}

func TestScheduledContextWorkflowRejectsAnOptInThatWillNotEnableTheTest(t *testing.T) {
	workflow := `jobs:
  context-matrix:
    if: github.event_name == 'schedule'
    steps:
      - env:
          OCH_EVAL_SCHEDULED_CONTEXT_MATRIX: "0"
        run: go test -race ./cmd/och-eval -run '^TestContextScheduledLane' -count=1
`
	if problems := strings.Join(scheduledContextEnvProblems(workflow), "\n"); !strings.Contains(problems, "literal value 1") {
		t.Fatalf("problems = %q; want the non-enabling value diagnosed", problems)
	}
}

func TestScheduledContextWorkflowRejectsAWorkflowWideOptIn(t *testing.T) {
	workflow := `env:
  OCH_EVAL_SCHEDULED_CONTEXT_MATRIX: "1"
jobs:
  go:
    steps:
      - run: go test -race ./... -count=1
  context-matrix:
    if: github.event_name == 'schedule'
    steps:
      - env:
          OCH_EVAL_SCHEDULED_CONTEXT_MATRIX: "1"
        run: go test -race ./cmd/och-eval -run '^TestContextScheduledLane' -count=1
`
	if problems := strings.Join(scheduledContextEnvProblems(workflow), "\n"); !strings.Contains(problems, "exactly one") {
		t.Fatalf("problems = %q; want the workflow-wide duplicate diagnosed", problems)
	}
}

// scheduledContextEnvProblems scans the entire workflow, not only its job
// blocks. A workflow-level env assignment is inherited by every job, including
// the broad PR lanes, so it must not be invisible to the wiring guard.
func scheduledContextEnvProblems(workflow string) []string {
	assignment := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(scheduledContextMatrixEnv) + `[ \t]*:[ \t]*([^\r\n]*)$`)
	matches := assignment.FindAllStringSubmatch(workflow, -1)
	if len(matches) != 1 {
		return []string{"the workflow must contain exactly one Context matrix opt-in assignment"}
	}

	// These are the three YAML spellings that reach the test process as the
	// exact string "1". Expressions and every other value fail closed because
	// the test binary itself enables the matrix only for that value.
	value := strings.TrimSpace(matches[0][1])
	switch value {
	case "1", `"1"`, "'1'":
		return nil
	default:
		return []string{"the Context matrix opt-in assignment must be the literal value 1"}
	}
}

// TestFullContextMatrixSkipsWithoutTheOptIn proves the default-off claim by
// running it rather than asserting it: this test binary is re-invoked with
// the opt-in removed from the environment, and the matrix test must report
// SKIP.
//
// Re-invoking os.Args[0] keeps the check honest without a source scan. If
// someone deletes the gate, the child does not skip — it starts building an
// och binary and running nine EvalSets — and this test fails on the missing
// SKIP or on the deadline, either way naming the regression.
func TestFullContextMatrixSkipsWithoutTheOptIn(t *testing.T) {
	const matrixTest = "TestContextScheduledLaneRunsEveryPairedSet"

	// A skip is immediate; anything approaching this deadline means the gate
	// is gone and the real matrix has started.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, os.Args[0],
		"-test.run", "^"+matrixTest+"$", "-test.v")
	command.Env = environmentWithout(os.Environ(), scheduledContextMatrixEnv)

	output, err := command.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("%s did not skip without %s — it kept running, so the opt-in gate is gone",
			matrixTest, scheduledContextMatrixEnv)
	}
	if err != nil {
		t.Fatalf("re-running %s: %v\n%s", matrixTest, err, output)
	}
	if !strings.Contains(string(output), "--- SKIP: "+matrixTest) {
		t.Fatalf("%s ran without %s set; it must skip.\n%s",
			matrixTest, scheduledContextMatrixEnv, output)
	}
}

// environmentWithout returns environ with every assignment of name removed.
func environmentWithout(environ []string, name string) []string {
	kept := make([]string, 0, len(environ))
	for _, assignment := range environ {
		if strings.HasPrefix(assignment, name+"=") {
			continue
		}
		kept = append(kept, assignment)
	}
	return kept
}

// workflowJob is one job block of .github/workflows/ci.yml, kept as raw lines
// so the guard needs no YAML dependency. The repository pins its dependency
// graph with `go mod tidy -diff` and govulncheck; a parser is not worth a new
// module for four assertions over a file this project writes itself.
type workflowJob struct {
	name  string
	lines []string
}

func (job workflowJob) text() string { return strings.Join(job.lines, "\n") }

func (job workflowJob) setsEnv(name string) bool {
	return regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `\s*:`).MatchString(job.text())
}

var (
	jobHeader       = regexp.MustCompile(`^  ([A-Za-z][A-Za-z0-9_-]*):\s*$`)
	goTestInvoke    = regexp.MustCompile(`go test [^\n]*`)
	scheduleOnlyIf  = regexp.MustCompile(`(?m)^\s*if:\s*github\.event_name == 'schedule'\s*$`)
	wholeSuiteMatch = regexp.MustCompile(`go test [^\n]*\./\.\.\.`)
)

func ciWorkflowText(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return readRepoFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
}

// ciWorkflowJobs splits the workflow into its top-level jobs.
func ciWorkflowJobs(t *testing.T) []workflowJob {
	t.Helper()
	workflow := ciWorkflowText(t)

	var jobs []workflowJob
	var current *workflowJob
	inJobs := false
	for _, line := range strings.Split(workflow, "\n") {
		if line == "jobs:" {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if match := jobHeader.FindStringSubmatch(line); match != nil {
			jobs = append(jobs, workflowJob{name: match[1]})
			current = &jobs[len(jobs)-1]
			continue
		}
		if current != nil {
			current.lines = append(current.lines, line)
		}
	}
	if len(jobs) == 0 {
		t.Fatal("parsed no jobs out of .github/workflows/ci.yml")
	}
	return jobs
}

// TestCIEnablesTheFullContextMatrixOnlyInAScheduledJob is the executable form
// of the boundary three documents previously only asserted in prose.
func TestCIEnablesTheFullContextMatrixOnlyInAScheduledJob(t *testing.T) {
	for _, problem := range scheduledContextEnvProblems(ciWorkflowText(t)) {
		t.Error(problem)
	}
	jobs := ciWorkflowJobs(t)

	var enabling []workflowJob
	for _, job := range jobs {
		if job.setsEnv(scheduledContextMatrixEnv) {
			enabling = append(enabling, job)
		}
	}
	if len(enabling) != 1 {
		names := make([]string, 0, len(enabling))
		for _, job := range enabling {
			names = append(names, job.name)
		}
		t.Fatalf("%d CI jobs set %s (%v); exactly one may",
			len(enabling), scheduledContextMatrixEnv, names)
	}
	matrix := enabling[0]

	if !scheduleOnlyIf.MatchString(matrix.text()) {
		t.Errorf("job %q sets %s without `if: github.event_name == 'schedule'`, "+
			"so the pull-request path would run the full matrix",
			matrix.name, scheduledContextMatrixEnv)
	}

	invocations := goTestInvoke.FindAllString(matrix.text(), -1)
	if len(invocations) != 1 {
		t.Fatalf("job %q runs %d `go test` commands; the matrix lane runs exactly one focused command",
			matrix.name, len(invocations))
	}
	invocation := invocations[0]
	for _, required := range []string{"./cmd/och-eval", "-run '^TestContextScheduledLane", "-count=1"} {
		if !strings.Contains(invocation, required) {
			t.Errorf("job %q runs %q, which omits %q; the matrix lane must be focused and run once",
				matrix.name, invocation, required)
		}
	}
	if wholeSuiteMatch.MatchString(invocation) {
		t.Errorf("job %q runs the whole suite (%q) with %s set; it must run only the focused lane",
			matrix.name, invocation, scheduledContextMatrixEnv)
	}
}

// TestBroadSuiteJobsNeverEnableTheFullContextMatrix states the other half
// positively: the jobs that do run `go test ./...` — `go` on every pull
// request, `determinism` at -count=3, `soak` at -count=10 — must not carry
// the opt-in. Those three repetitions are exactly what made the unguarded
// matrix expensive.
func TestBroadSuiteJobsNeverEnableTheFullContextMatrix(t *testing.T) {
	var broad []string
	for _, job := range ciWorkflowJobs(t) {
		if !wholeSuiteMatch.MatchString(job.text()) {
			continue
		}
		broad = append(broad, job.name)
		if job.setsEnv(scheduledContextMatrixEnv) {
			t.Errorf("job %q runs the whole suite and sets %s; the full matrix would run in it",
				job.name, scheduledContextMatrixEnv)
		}
	}
	for _, required := range []string{"go", "determinism", "soak"} {
		if !contains(broad, required) {
			t.Errorf("expected job %q to still run the whole suite; found %v", required, broad)
		}
	}
}

// checkedInContextSets returns every checked-in EvalSet file, decoded.
func checkedInContextSets(t *testing.T) map[string]documentTree {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	setsDir := filepath.Join(root, "eval", "sets")
	entries, err := os.ReadDir(setsDir)
	if err != nil {
		t.Fatalf("read eval/sets: %v", err)
	}
	trees := map[string]documentTree{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		tree, err := loadDocumentTree(filepath.Join(setsDir, entry.Name()))
		if err != nil {
			t.Fatalf("loadDocumentTree(%s): %v", entry.Name(), err)
		}
		trees[entry.Name()] = tree
	}
	if len(trees) == 0 {
		t.Fatal("found no checked-in EvalSets")
	}
	return trees
}

// TestScheduledLaneCoversEveryCheckedInContextSet keeps contextScheduledSets
// from silently falling behind the sets on disk.
//
// The list is maintained by hand, so a tenth Context set added later would
// simply never run: the lane would still pass, having quietly stopped being
// the full matrix. Membership is decided by two independent facts rather than
// by a filename convention alone — the set's own declared `fixture` lane, and
// the `context-` id prefix that separates the scheduled sets from the PR
// lane's own `pr-context`.
func TestScheduledLaneCoversEveryCheckedInContextSet(t *testing.T) {
	var expected []string
	for name, tree := range checkedInContextSets(t) {
		if tree.Set.Lane != eval.LaneFixture {
			continue
		}
		if !strings.HasPrefix(string(tree.Set.ID), "context-") {
			continue
		}
		expected = append(expected, name)
	}
	sort.Strings(expected)

	scheduled := append([]string(nil), contextScheduledSets...)
	sort.Strings(scheduled)

	if !slices.Equal(expected, scheduled) {
		t.Errorf("the scheduled lane runs %v but the repository holds %v;\n"+
			"a fixture-lane context-* EvalSet that is not in contextScheduledSets never runs anywhere",
			scheduled, expected)
	}
}

// TestEveryInProcessContextSetHasAnIdenticalACPArm holds the suite design's
// pairing claim — every Context Scenario is exercised through both execution
// surfaces — as a structural fact rather than as a property of how the sets
// happened to be written.
//
// context-recovery-acp has no in-process arm by design: restart recovery is
// only meaningful against a real subprocess.
func TestEveryInProcessContextSetHasAnIdenticalACPArm(t *testing.T) {
	trees := checkedInContextSets(t)
	for _, name := range unexpectedACPOnlyContextSets(trees) {
		t.Errorf("%s has no in-process twin; only context-recovery-acp.json may be ACP-only", name)
	}

	paired := 0
	for name, inProcess := range trees {
		arm, found := strings.CutSuffix(name, "-inprocess.json")
		if !found || !strings.HasPrefix(arm, "context-") {
			continue
		}
		acpName := arm + "-acp.json"
		acp, ok := trees[acpName]
		if !ok {
			t.Errorf("%s has no %s; the suite design pairs every Context Scenario with the ACP executor", name, acpName)
			continue
		}
		paired++

		if !slices.Equal(scenarioIDs(inProcess), scenarioIDs(acp)) {
			t.Errorf("%s runs %v but %s runs %v; a paired arm must carry the identical Scenario list",
				name, scenarioIDs(inProcess), acpName, scenarioIDs(acp))
		}
		if kind := soleExecutorKind(t, inProcess); kind != eval.ExecutorInProcess {
			t.Errorf("%s runs a %q executor, want %q", name, kind, eval.ExecutorInProcess)
		}
		if kind := soleExecutorKind(t, acp); kind != eval.ExecutorACPSubprocess {
			t.Errorf("%s runs a %q executor, want %q", acpName, kind, eval.ExecutorACPSubprocess)
		}
	}
	if paired == 0 {
		t.Fatal("found no paired in-process Context EvalSets; this guard is no longer reading the sets")
	}
}

func TestOnlyRecoveryMayBeAnACPOnlyContextSet(t *testing.T) {
	trees := map[string]documentTree{
		"context-recovery-acp.json": {},
		"context-orphan-acp.json":   {},
	}
	if unpaired := unexpectedACPOnlyContextSets(trees); len(unpaired) != 1 || unpaired[0] != "context-orphan-acp.json" {
		t.Fatalf("unexpected ACP-only sets = %v, want [context-orphan-acp.json]", unpaired)
	}
}

// unexpectedACPOnlyContextSets returns every Context ACP arm other than the
// recovery exception that has no checked-in in-process twin.
func unexpectedACPOnlyContextSets(trees map[string]documentTree) []string {
	var unexpected []string
	for name := range trees {
		arm, found := strings.CutSuffix(name, "-acp.json")
		if !found || !strings.HasPrefix(arm, "context-") || name == "context-recovery-acp.json" {
			continue
		}
		if _, ok := trees[arm+"-inprocess.json"]; !ok {
			unexpected = append(unexpected, name)
		}
	}
	sort.Strings(unexpected)
	return unexpected
}

func scenarioIDs(tree documentTree) []string {
	ids := make([]string, 0, len(tree.Set.Scenarios))
	for _, ref := range tree.Set.Scenarios {
		ids = append(ids, string(ref.ID))
	}
	sort.Strings(ids)
	return ids
}

func soleExecutorKind(t *testing.T, tree documentTree) eval.ExecutorKind {
	t.Helper()
	if len(tree.Set.Executors) != 1 {
		t.Fatalf("EvalSet %s declares %d executors; a Context arm declares exactly one",
			tree.Set.ID, len(tree.Set.Executors))
	}
	return tree.Executors[tree.Set.Executors[0].ID].Kind
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
