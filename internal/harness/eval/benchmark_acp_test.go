//go:build unix

package eval

import (
	"context"
	"testing"
)

// BenchmarkACPProcessStartupAndShutdown measures design §16's own
// subprocess orchestration cost in isolation from any model call: spawn
// a real och -acp process, complete the ACP handshake, then perform the
// normal shutdown sequence (close stdin, wait for reap) — the same
// startACPProcess+initialize+closeAndReapACP path RunACPAttempt itself
// uses, with no Scenario action (and so no provider request) ever run.
// This isolates process-lifecycle latency from model latency, which the
// plan itself calls out as easy to conflate.
func BenchmarkACPProcessStartupAndShutdown(b *testing.B) {
	ochBin := buildOchBinary(b)
	binary, err := ResolveACPBinary(ochBin)
	if err != nil {
		b.Fatalf("ResolveACPBinary: %v", err)
	}
	server := newEchoProvider(b)
	subject := testSubject(b, server.Server)
	env, err := BuildChildEnvironment(subject)
	if err != nil {
		b.Fatalf("BuildChildEnvironment: %v", err)
	}
	argv, err := NormalizedArgv(subject)
	if err != nil {
		b.Fatalf("NormalizedArgv: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		workspace := b.TempDir()
		attemptID, err := NewAttemptID()
		if err != nil {
			b.Fatalf("NewAttemptID: %v", err)
		}
		directories, err := NewAttemptRoot(workspace, attemptID)
		if err != nil {
			b.Fatalf("NewAttemptRoot: %v", err)
		}
		fullArgv := append([]string{
			"-acp", "-workspace", directories.Workspace, "-database", AttemptDatabasePath(directories),
			"-runtime-id", launchRuntimeID(attemptID, 0), "-audit-dir", directories.Audit,
		}, argv...)

		process, err := startACPProcess(binary.Path, fullArgv, env, directories.Workspace, defaultACPStderrBytes)
		if err != nil {
			b.Fatalf("startACPProcess: %v", err)
		}
		conn := newACPConnection(process.stdout, process.stdin, discardPermissionHandler)
		initCtx, cancel := context.WithTimeout(context.Background(), DefaultProcessStartup)
		if _, err := conn.initialize(initCtx); err != nil {
			cancel()
			b.Fatalf("initialize: %v", err)
		}
		cancel()
		if !closeAndReapACP(conn, process, DefaultShutdownGrace) {
			b.Fatalf("closeAndReapACP did not prove the writer stopped")
		}
	}
}
