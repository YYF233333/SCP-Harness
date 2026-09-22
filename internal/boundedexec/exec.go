package boundedexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

type Command struct {
	Argv []string
	Dir  string
	Env  []string
	// ReplaceEnv is used for archive isolation; other commands inherit the host.
	ReplaceEnv           bool
	Stdin                io.Reader
	Timeout              time.Duration
	MaxStdout, MaxStderr int64
	// StdoutSink supports bounded streaming archives without accumulating in RAM.
	StdoutSink io.Writer
	StderrSink io.Writer
}
type Result struct {
	StdoutTruncated, StderrTruncated            bool
	Stdout, Stderr                              []byte
	ExitCode                                    int
	Started, TimedOut, Canceled, OutputExceeded bool
	Elapsed                                     time.Duration
}
type capWriter struct {
	dst      io.Writer
	left     int64
	exceeded bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	n := len(p)
	if int64(n) > w.left {
		w.exceeded = true
		p = p[:w.left]
	}
	if len(p) > 0 {
		m, e := w.dst.Write(p)
		w.left -= int64(m)
		if e != nil {
			return m, e
		}
		if m != len(p) {
			return m, io.ErrShortWrite
		}
	}
	return n, nil
}

// Run always bounds process duration and both output streams. Capped output is
// drained/discarded, never accumulated. Context cancellation is out-of-band.
func Run(ctx context.Context, c Command) (Result, error) {
	var r Result
	r.ExitCode = -1
	if len(c.Argv) == 0 || c.Argv[0] == "" || c.Timeout <= 0 || c.MaxStdout <= 0 || c.MaxStderr <= 0 {
		return r, fmt.Errorf("boundedexec requires command and finite bounds")
	}
	timed, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	cmd := exec.CommandContext(timed, c.Argv[0], c.Argv[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	if c.ReplaceEnv {
		cmd.Env = c.Env
	}
	cmd.Stdin = c.Stdin
	var out, errout bytes.Buffer
	sink := c.StdoutSink
	if sink == nil {
		sink = &out
	}
	ow := &capWriter{dst: sink, left: c.MaxStdout}
	errSink := c.StderrSink
	if errSink == nil {
		errSink = &errout
	}
	ew := &capWriter{dst: errSink, left: c.MaxStderr}
	cmd.Stdout = ow
	cmd.Stderr = ew
	cmd.WaitDelay = time.Second
	configure(cmd)
	start := time.Now()
	if e := cmd.Start(); e != nil {
		r.Elapsed = time.Since(start)
		return r, e
	}
	r.Started = true
	cleanup, e := contain(cmd.Process)
	if e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return r, e
	}
	defer cleanup()
	e = cmd.Wait()
	r.Elapsed = time.Since(start)
	r.Stdout = out.Bytes()
	r.Stderr = errout.Bytes()
	r.StdoutTruncated, r.StderrTruncated = ow.exceeded, ew.exceeded
	r.OutputExceeded = ow.exceeded || ew.exceeded
	r.TimedOut = errors.Is(timed.Err(), context.DeadlineExceeded)
	r.Canceled = ctx.Err() != nil
	if cmd.ProcessState != nil {
		r.ExitCode = cmd.ProcessState.ExitCode()
	}
	if _, ok := e.(*exec.ExitError); ok {
		e = nil
	}
	if r.TimedOut || r.Canceled {
		e = nil
	}
	return r, e
}
