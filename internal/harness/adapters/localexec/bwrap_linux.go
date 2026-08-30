//go:build linux

package localexec

import (
	"os"
	"os/exec"
	"strings"
)

// probeBwrap resolves bwrap on PATH and confirms one scoped invocation
// actually succeeds, distinguishing WSL1 (bubblewrap is unsupported there,
// the same distinction Codex's own Wsl1UnsupportedForBubblewrap names) from
// a missing binary or a probe that runs but fails, so a diagnostic can name
// which one it hit rather than collapsing all three into "unavailable".
func probeBwrap() (available bool, reason string) {
	if isWSL1() {
		return false, "wsl1: bubblewrap is unsupported under WSL1"
	}
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return false, "bwrap not found on PATH"
	}
	probe := exec.Command(path, "--unshare-user", "--unshare-pid", "--ro-bind", "/", "/", "--proc", "/proc", "true")
	if err := probe.Run(); err != nil {
		return false, "bwrap probe failed: " + err.Error()
	}
	return true, ""
}

func isWSL1() bool {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return isWSL1Version(string(data))
}

// isWSL1Version reports whether the contents of /proc/version identify a
// WSL1 kernel: Microsoft's Linux-compatibility kernel names itself
// "Microsoft" in this string, and WSL2 (a real Linux kernel) additionally
// names "WSL2" there, so a "Microsoft" without a "WSL2" is WSL1.
func isWSL1Version(version string) bool {
	return strings.Contains(version, "Microsoft") && !strings.Contains(version, "WSL2")
}

// bwrapArgv wraps target in the namespace set this project confines every
// Linux exec call with when bwrap is available: process/network/IPC/UTS/
// cgroup isolation, every capability dropped, a read-only view of the host
// with the workspace root rebound read-write at the same path, per design
// doc §3.2. bwrap itself is not part of this argv; the caller runs it as
// the command.
func bwrapArgv(workspace, cwd string, target []string) []string {
	argv := []string{
		"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts",
		"--unshare-cgroup", "--unshare-net",
		"--die-with-parent", "--new-session",
		"--cap-drop", "ALL",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
		"--bind", workspace, workspace,
		"--chdir", cwd,
	}
	return append(argv, target...)
}
