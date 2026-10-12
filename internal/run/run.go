// Package run executes external programs (qemu-img, qemu-nbd, mount, packer,
// 7z) behind an interface, so code that drives them can be tested by
// recording the commands instead of running them.
package run

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// Runner runs a command and returns its combined output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
	// Stream runs a command with its output going to w as it is produced
	// (long-running tools such as packer).
	Stream(ctx context.Context, w io.Writer, name string, args ...string) error
}

// Exec runs real programs.
type Exec struct{}

func (Exec) Run(ctx context.Context, name string, args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s: %w: %s", Format(name, args...), err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

func (Exec) Stream(ctx context.Context, w io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", Format(name, args...), err)
	}
	return nil
}

// Format renders a command line for messages and test expectations.
func Format(name string, args ...string) string {
	return strings.TrimSpace(name + " " + strings.Join(args, " "))
}

// Recorder records commands and fails those matching FailOn (substring).
type Recorder struct {
	mu       sync.Mutex
	Commands []string
	FailOn   string
	Output   map[string]string // command line -> output to return
}

func (r *Recorder) record(name string, args []string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	line := Format(name, args...)
	r.Commands = append(r.Commands, line)
	if r.FailOn != "" && strings.Contains(line, r.FailOn) {
		return "", fmt.Errorf("%s: simulated failure", line)
	}
	return r.Output[line], nil
}

func (r *Recorder) Run(_ context.Context, name string, args ...string) (string, error) {
	return r.record(name, args)
}

func (r *Recorder) Stream(_ context.Context, w io.Writer, name string, args ...string) error {
	out, err := r.record(name, args)
	io.WriteString(w, out)
	return err
}
