package snip

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type Runner interface {
	Output(context.Context, string, ...string) ([]byte, error)
	Run(context.Context, io.Reader, io.Writer, io.Writer, string, ...string) error
}

type execRunner struct{}

type commandError struct {
	Command string
	Detail  string
}

func (e commandError) Error() string {
	return fmt.Sprintf("%s: %s", e.Command, strings.TrimSpace(e.Detail))
}

func (execRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, commandError{Command: name, Detail: string(exit.Stderr)}
		}
		return nil, err
	}
	return out, nil
}

func (execRunner) Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}
