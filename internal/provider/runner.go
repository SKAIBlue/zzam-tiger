package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
	LookPath(string) error
}

// InputRunner is an optional Runner capability for commands that accept
// structured data on standard input.
type InputRunner interface {
	RunInput(context.Context, []byte, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

// CommandError preserves a subprocess exit code while keeping command output
// in the human-readable error message.
type CommandError struct {
	name string
	out  []byte
	err  error
}

func (e *CommandError) Error() string {
	message := strings.TrimSpace(string(e.out))
	if message == "" {
		message = e.err.Error()
	}
	return fmt.Sprintf("%s: %s", e.name, message)
}

func (e *CommandError) Unwrap() error { return e.err }

// IsExitCode reports whether err came from a subprocess with the given code.
func IsExitCode(err error, code int) bool {
	var exitError *exec.ExitError
	return errors.As(err, &exitError) && exitError.ExitCode() == code
}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return runCommand(name, cmd)
}

func (ExecRunner) RunInput(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(input)
	return runCommand(name, cmd)
}

func runCommand(name string, cmd *exec.Cmd) ([]byte, error) {
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, &CommandError{name: name, out: out, err: err}
	}
	return out, nil
}

func (ExecRunner) LookPath(name string) error {
	_, err := exec.LookPath(name)
	return err
}
