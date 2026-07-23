package provider

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
	LookPath(string) error
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
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
