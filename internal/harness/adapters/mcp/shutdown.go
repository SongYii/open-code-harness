//go:build unix

package mcp

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Shutdown grades. Each bounds one rung of the ladder below.
const (
	// GracefulGrace bounds the SDK's own stdio shutdown — close stdin, wait,
	// SIGTERM, Kill — before this package escalates past it. It is generous
	// because the overwhelming majority of servers exit at the first rung and
	// escalation is the exception, not the plan.
	GracefulGrace = 10 * time.Second

	// GroupTermGrace bounds the process group's chance to exit after SIGTERM.
	GroupTermGrace = 3 * time.Second

	// GroupKillGrace bounds the wait for the group to disappear after
	// SIGKILL. Exceeding it means teardown could not be proven, which is
	// reported rather than assumed away.
	GroupKillGrace = 5 * time.Second

	// livenessPoll is how often the group is re-probed while waiting.
	livenessPoll = 25 * time.Millisecond
)

// ErrTeardownUnproven marks a server whose process group could not be proven
// gone.
//
// It is a distinct error because "we signalled it" and "it is gone" are
// different facts, and only the second is safe to report as success. This
// project's ACP restart path already refuses to launch a successor until reap
// is proven; the same standard applies here.
var ErrTeardownUnproven = errors.New("mcp: server process group teardown could not be proven")

// shutdownProcess stops a server's subprocess and proves the whole group is
// gone.
//
// The ladder begins with the SDK's own shutdown, which closes stdin and lets
// the server exit on its terms — the specification's prescribed sequence, and
// what a well-behaved server expects. Two things the SDK does not do are this
// function's job:
//
//   - Its last rung is Process.Kill(), which signals the process alone. A
//     server that spawned children of its own leaves them orphaned and
//     running. This project's ACP executor states the reason plainly: a
//     ctx-triggered kill "can only reach a process's direct children, not the
//     whole process group". So escalation here signals the group, which the
//     confined command was given precisely so this would be possible.
//   - It returns without proving the group is gone. Signalling is not
//     collection.
//
// The SDK owns cmd.Wait() inside its own Close, so this function must not
// call Wait itself — a second Wait on the same command races the first and
// fails with "no child processes". Proof therefore comes from two places: a
// clean return from the SDK's close means its own Wait returned and the
// process was reaped, and past that point liveness is probed with signal 0
// against the process group.
func shutdownProcess(command *exec.Cmd, closeSession func() error) error {
	if command == nil || command.Process == nil {
		if closeSession != nil {
			return closeSession()
		}
		return nil
	}
	pid := command.Process.Pid

	var sessionErr error
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		if closeSession != nil {
			sessionErr = closeSession()
		}
	}()

	select {
	case <-closed:
		if sessionErr == nil {
			// The SDK's own Close returned cleanly, which means its Wait
			// returned: the process was collected. Children it spawned are
			// still this function's problem, so the group is checked anyway.
			if groupGone(pid, GroupTermGrace) {
				return nil
			}
		}
	case <-time.After(GracefulGrace):
		// A stuck graceful path is exactly what the group signals exist for.
	}

	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		// An unexpected signal failure is worth reporting alongside whatever
		// the session said, but escalation continues regardless.
		sessionErr = errors.Join(sessionErr, fmt.Errorf("mcp: SIGTERM to group %d: %w", pid, err))
	}
	if groupGone(pid, GroupTermGrace) {
		return sessionErrOrNil(sessionErr)
	}

	_ = syscall.Kill(-pid, syscall.SIGKILL)
	if groupGone(pid, GroupKillGrace) {
		return sessionErrOrNil(sessionErr)
	}

	return errors.Join(sessionErrOrNil(sessionErr), fmt.Errorf("%w: group %d", ErrTeardownUnproven, pid))
}

// groupGone reports whether both the process group and the leader itself are
// gone, polling until timeout.
//
// Signal 0 performs the kernel's own permission and existence check without
// delivering anything, which is how a caller that does not own wait() can
// still establish that a process no longer exists.
//
// Both checks are required, and the second is not redundant. A confined
// command is always created as its own group leader, so pgid equals pid — but
// if that ever stops holding, kill(-pid, 0) addresses a group that may not
// exist and returns ESRCH while the process is alive and well. Treating that
// as proof would report a false success, which is the exact class of claim
// this ladder exists to eliminate. A still-unreaped zombie also answers
// kill(pid, 0) successfully, so a process whose wait is owned elsewhere
// correctly reads as not-yet-gone rather than as collected.
func groupGone(pid int, timeout time.Duration) bool {
	gone := func(target int) bool {
		return errors.Is(syscall.Kill(target, 0), syscall.ESRCH)
	}
	deadline := time.Now().Add(timeout)
	for {
		if gone(-pid) && gone(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(livenessPoll)
	}
}

// sessionErrOrNil discards the session errors that are the expected
// consequence of terminating a server on purpose.
//
// A server this package killed reports its transport as broken and its own
// Wait as a signal exit. Surfacing those to an operator as failures would
// mean every deliberate shutdown of a stubborn server looked like a fault.
func sessionErrOrNil(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, expected := range []string{
		"no child processes",
		"unresponsive subprocess",
		"signal:",
		"file already closed",
		"broken pipe",
		"EOF",
	} {
		if strings.Contains(message, expected) {
			return nil
		}
	}
	return err
}
