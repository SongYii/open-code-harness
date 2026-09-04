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
	"strings"
	"testing"
	"time"
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

// ciWorkflowJobs splits the workflow into its top-level jobs.
func ciWorkflowJobs(t *testing.T) []workflowJob {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	workflow := readRepoFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))

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

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
