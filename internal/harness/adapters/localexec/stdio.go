package localexec

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// ErrProcessTeardownUnproven means the process tree could not be proven gone.
// Callers must not treat it as successful resource release.
var ErrProcessTeardownUnproven = errors.New("localexec: process teardown could not be proven")

// StdioProcess owns a long-lived confined process, its byte channel and its
// single Wait. It knows nothing about the protocol carried on that channel.
// Close is concurrent/idempotent and caches failure as well as success.
type StdioProcess struct {
	command   *ConfinedCommand
	mu        sync.Mutex
	started   bool
	closed    bool
	stdin     *os.File
	stdout    *os.File
	waited    chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// NewStdioProcess shares Run's admission, confinement and environment. The
// result owns temporary resources even before Start and must always be closed.
func (r *Runner) NewStdioProcess(spec tools.CommandSpec) (*StdioProcess, error) {
	if !stdioProcessSupported {
		return nil, errors.New("localexec: managed stdio processes require POSIX supervision")
	}
	command, err := r.NewConfinedCommand(spec)
	if err != nil {
		return nil, err
	}
	return &StdioProcess{command: command}, nil
}

// Start is a single attempt, serialized with Close. ctx governs startup only;
// the owner explicitly closes the successfully started resource after drain.
func (p *StdioProcess) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.started {
		return errConfinedClosed
	}
	if ctx == nil {
		return errors.New("localexec: startup context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.started = true
	childIn, parentIn, err := os.Pipe()
	if err != nil {
		return err
	}
	defer childIn.Close()
	p.stdin = parentIn
	parentOut, childOut, err := os.Pipe()
	if err != nil {
		return err
	}
	defer childOut.Close()
	p.stdout = parentOut
	cmd := p.command.cmd
	cmd.Stdin, cmd.Stdout = childIn, childOut
	// Stderr remains discarded, as on the former CommandTransport path.
	err = func() error {
		release := p.command.StartBracket()
		defer release()
		return cmd.Start()
	}()
	if err != nil {
		return err
	}
	p.waited = make(chan struct{})
	go func() {
		// Exit status is not teardown proof; the channel plus process-group
		// liveness is. Protocol errors remain the protocol consumer's job.
		_ = cmd.Wait()
		close(p.waited)
	}()
	_ = p.command.Register(cmd.Process.Pid) // existing best-effort quota policy
	return ctx.Err()
}

func (p *StdioProcess) Read(b []byte) (int, error) {
	p.mu.Lock()
	r := p.stdout
	p.mu.Unlock()
	if r == nil {
		return 0, io.ErrClosedPipe
	}
	return r.Read(b)
}

func (p *StdioProcess) Write(b []byte) (int, error) {
	p.mu.Lock()
	w := p.stdin
	p.mu.Unlock()
	if w == nil {
		return 0, io.ErrClosedPipe
	}
	return w.Write(b)
}

// Close first sends EOF, then escalates to the process group if necessary.
// It closes parent pipes even on unproven teardown, unblocking channel users.
func (p *StdioProcess) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		in, out, waited := p.stdin, p.stdout, p.waited
		p.mu.Unlock()
		if in != nil {
			_ = in.Close()
		}
		if waited != nil {
			p.closeErr = stopStdioProcess(p.command.cmd.Process.Pid, waited)
		}
		if out != nil {
			_ = out.Close()
		}
		p.closeErr = errors.Join(p.closeErr, p.command.Close())
	})
	return p.closeErr
}
