// Package bash provides the bash tool: it runs a shell command on the model's
// behalf. BashTool implements tools.Tool by structural typing, so this package
// does not import tools.
//
// This is the most powerful tool in the harness and is intentionally
// unsandboxed: it runs with the user's privileges in the working directory. See
// the README's security section before pointing klauscode at untrusted input.
package bash

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"klauscode/internal/tools/textutil"
)

const (
	// defaultTimeout bounds a single command so a hung process cannot stall the
	// run forever.
	defaultTimeout = 30 * time.Second
	// maxOutput caps combined stdout+stderr fed back to the model.
	maxOutput = 160 * 1024
)

// BashTool runs shell commands via `sh -c`.
type BashTool struct {
	timeout time.Duration
}

// New returns a ready-to-register bash tool with the default timeout.
func New() *BashTool { return &BashTool{timeout: defaultTimeout} }

// WithTimeout overrides the per-command timeout (used by tests).
func (t *BashTool) WithTimeout(d time.Duration) *BashTool {
	t.timeout = d
	return t
}

func (t *BashTool) Name() string { return "bash" }

func (t *BashTool) Description() string {
	return "bash(<shell command>): Run a shell command (sh -c) and return combined stdout+stderr. Put the command directly inside the parentheses, e.g. bash(ls -R) — not bash(command=\"ls -R\"). Use for ls, grep, go build, go test."
}

// Call runs args as a shell command and returns its combined output. A non-zero
// exit or a timeout is returned as a normal result (not a Go error) with a note,
// because that output is signal the model must read to self-correct. Only a
// harness-level failure (e.g. sh missing) is returned as an error.
func (t *BashTool) Call(args string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", args)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	runErr := cmd.Run()
	out := textutil.Truncate(buf.String(), maxOutput)

	if ctx.Err() == context.DeadlineExceeded {
		return out + fmt.Sprintf("\n[timed out after %s]", t.timeout), nil
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return out + fmt.Sprintf("\n[exit code: %d]", exitErr.ExitCode()), nil
		}
		// Could not start the command at all (e.g. sh not found).
		return "", fmt.Errorf("bash: %w", runErr)
	}
	return out, nil
}
