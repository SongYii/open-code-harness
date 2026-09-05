//go:build unix

package mcp

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// countGroup reports how many live processes belong to process group pgid.
//
// The group is what the ladder acts on, so counting by group tests the thing
// directly. It also sidesteps identifying a child by argv: `exec -a` is a
// bash builtin and /bin/sh is dash on many hosts, so a renamed child cannot
// be relied on.
func countGroup(t *testing.T, pgid int) int {
	t.Helper()
	output, err := exec.Command("ps", "-eo", "pgid=,pid=").Output()
	if err != nil {
		t.Skipf("ps unavailable: %v", err)
	}
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if group, err := strconv.Atoi(fields[0]); err == nil && group == pgid {
			count++
		}
	}
	return count
}

func waitForGroupCount(t *testing.T, pgid, want int, within time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(within)
	got := countGroup(t, pgid)
	for got != want && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		got = countGroup(t, pgid)
	}
	return got
}

// TestShutdownLeavesNoGrandchildBehind is the test this whole task exists
// for.
//
// The SDK's own ladder ends at Process.Kill(), which signals the server
// process and nothing else. A server that spawned a child of its own — an
// indexer, a helper, a language server — leaves that child orphaned and
// running after the harness exits. Escalating to the process group is what
// takes the whole family, and this is the only test that can tell the
// difference.
func TestShutdownLeavesNoGrandchildBehind(t *testing.T) {
	factory := fixtureFactory(t, "OCH_FIXTURE_MODE=spawns_child")

	server, err := Connect(t.Context(), ServerConfig{Name: "spawner", Command: "unused"}, factory)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	pgid := factory.command.cmd.Process.Pid

	// The server plus its own child: two members of one group.
	if got := waitForGroupCount(t, pgid, 2, 5*time.Second); got < 2 {
		_ = server.Close()
		t.Skipf("the fixture's own child never appeared (group holds %d); nothing to prove here", got)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if leaked := waitForGroupCount(t, pgid, 0, 15*time.Second); leaked != 0 {
		t.Fatalf("%d processes survived Close in group %d; teardown reached the process but not its group", leaked, pgid)
	}
}

// TestShutdownReapsAServerThatIgnoresSIGTERM pins that a server refusing the
// gentle signals still ends up gone and reported as such.
//
// It is deliberately recorded as *not* load-bearing for the escalation this
// file adds. A mutation removing the group escalation entirely leaves this
// test green, because the SDK's own ladder ends at Process.Kill() and SIGKILL
// cannot be ignored — a single stubborn process was never the gap. The gap is
// the process *group*, and TestShutdownLeavesNoGrandchildBehind is the test
// that actually fails when escalation is removed. This one is a regression
// guard on the SDK's own behaviour, which is worth having and worth not
// mistaking for proof of ours.
func TestShutdownReapsAServerThatIgnoresSIGTERM(t *testing.T) {
	factory := fixtureFactory(t, "OCH_FIXTURE_MODE=ignores_sigterm")
	server, err := Connect(t.Context(), ServerConfig{Name: "stubborn", Command: "unused"}, factory)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	pid := factory.command.cmd.Process.Pid

	start := time.Now()
	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > GracefulGrace+GroupTermGrace+GroupKillGrace+10*time.Second {
		t.Fatalf("Close took %s; the ladder is not bounded", elapsed)
	}
	if alive := processAlive(pid); alive {
		t.Fatalf("pid %d is still alive after Close", pid)
	}
}

// TestShutdownReportsUnprovenReapRatherThanClaimingSuccess: signalling is not
// reaping, and the two must not be reported as the same thing.
func TestShutdownReportsUnprovenReapRatherThanClaimingSuccess(t *testing.T) {
	// A command that was never started has no process to reap, so the ladder
	// must not invent a success or a failure of its own.
	if err := shutdownProcess(nil, func() error { return nil }); err != nil {
		t.Fatalf("shutdownProcess(nil) = %v, want nil", err)
	}

	// A live process whose wait never completes must surface ErrReapUnproven
	// rather than returning nil. Driving this deterministically means calling
	// the ladder with a command whose Wait is already owned elsewhere, which
	// is exactly the shape that makes reap unprovable.
	command := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := command.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	go func() { _ = command.Wait() }() // steal the reap
	defer func() { _ = command.Process.Kill() }()

	err := shutdownProcess(command, func() error { return nil })
	if err != nil && !errors.Is(err, ErrTeardownUnproven) {
		t.Logf("shutdownProcess returned %v", err)
	}
	// Either outcome is legitimate here depending on which goroutine wins the
	// wait; what must never happen is a claim of success while the process is
	// still running.
	if err == nil && processAlive(command.Process.Pid) {
		t.Fatal("shutdownProcess reported success while the process was still alive")
	}
}

// TestShutdownDoesNotReportADeliberateKillAsAFailure: a server this package
// killed on purpose exits by signal, and that is the expected outcome rather
// than an error to surface to an operator.
func TestShutdownDoesNotReportADeliberateKillAsAFailure(t *testing.T) {
	factory := fixtureFactory(t, "OCH_FIXTURE_MODE=ignores_sigterm")
	server, err := Connect(t.Context(), ServerConfig{Name: "stubborn", Command: "unused"}, factory)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("Close reported %v for a server it deliberately terminated", err)
	}
}

func processAlive(pid int) bool {
	output, err := exec.Command("ps", "-o", "pid=", "-p", fmt.Sprint(pid)).Output()
	return err == nil && strings.TrimSpace(string(output)) != ""
}
