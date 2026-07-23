package provider

import (
	"bytes"
	"context"
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
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("%s: %s", name, message)
	}
	return out, nil
}

func (ExecRunner) LookPath(name string) error {
	_, err := exec.LookPath(name)
	return err
}
