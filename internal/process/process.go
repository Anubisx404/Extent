package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const DefaultTimeout = 2 * time.Minute
const DefaultMaxOutput = 1 << 20

type Result struct {
	Stdout, Stderr                   string
	StdoutTruncated, StderrTruncated bool
	ExitCode                         int
	Duration                         time.Duration
	TimedOut, Cancelled              bool
	Err                              error
}
type Runner struct {
	Timeout   time.Duration
	MaxOutput int
	Dir       string
	Env       []string
}

func (r Runner) Run(ctx context.Context, executable string, args ...string) Result {
	start := time.Now()
	out := Result{ExitCode: -1}
	if strings.TrimSpace(executable) == "" {
		out.Err = errors.New("process executable cannot be empty")
		out.Duration = time.Since(start)
		return out
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(c, executable, args...)
	cmd.Dir = r.Dir
	if len(r.Env) > 0 {
		cmd.Env = overlayEnvironment(os.Environ(), r.Env)
	}
	max := r.MaxOutput
	if max <= 0 {
		max = DefaultMaxOutput
	}
	var so, se limitedBuffer
	so.max = max
	se.max = max
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	out.Stdout, out.Stderr = so.String(), se.String()
	out.StdoutTruncated, out.StderrTruncated = so.truncated, se.truncated
	out.Duration = time.Since(start)
	if err == nil {
		out.ExitCode = 0
		return out
	}
	out.Err = fmt.Errorf("process %s: %w", executable, err)
	if c.Err() == context.DeadlineExceeded {
		out.TimedOut = true
	}
	if c.Err() == context.Canceled {
		out.Cancelled = true
	}
	if ee := (&exec.ExitError{}); errors.As(err, &ee) {
		out.ExitCode = ee.ExitCode()
	}
	return out
}

type limitedBuffer struct {
	data      []byte
	max       int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(b.data) < b.max {
		n := b.max - len(b.data)
		if n > len(p) {
			n = len(p)
		}
		b.data = append(b.data, p[:n]...)
		if n < len(p) {
			b.truncated = true
		}
	} else {
		b.truncated = true
	}
	return len(p), nil
}
func (b *limitedBuffer) String() string { return string(b.data) }

func overlayEnvironment(base, overlay []string) []string {
	result := append([]string(nil), base...)
	index := map[string]int{}
	key := func(value string) string {
		name, _, _ := strings.Cut(value, "=")
		if runtime.GOOS == "windows" {
			return strings.ToUpper(name)
		}
		return name
	}
	for i, value := range result {
		index[key(value)] = i
	}
	for _, value := range overlay {
		name := key(value)
		if i, ok := index[name]; ok {
			result[i] = value
			continue
		}
		index[name] = len(result)
		result = append(result, value)
	}
	return result
}

var _ io.Writer = (*limitedBuffer)(nil)
