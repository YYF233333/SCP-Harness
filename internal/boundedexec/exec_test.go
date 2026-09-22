package boundedexec

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBoundedLiveSinks(t *testing.T) {
	if os.Getenv("SCP_BOUNDED_SINK_CHILD") == "1" {
		for i := 0; i < 1024; i++ {
			_, _ = os.Stdout.Write(bytes.Repeat([]byte("o"), 4096))
			_, _ = os.Stderr.Write(bytes.Repeat([]byte("e"), 4096))
		}
		os.Exit(0)
	}
	out, e := os.Create(filepath.Join(t.TempDir(), "stdout"))
	if e != nil {
		t.Fatal(e)
	}
	defer out.Close()
	errout, e := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if e != nil {
		t.Fatal(e)
	}
	defer errout.Close()
	r, e := Run(context.Background(), Command{Argv: []string{os.Args[0], "-test.run=^TestBoundedLiveSinks$"}, Env: []string{"SCP_BOUNDED_SINK_CHILD=1"}, StdoutSink: out, StderrSink: errout, Timeout: 20 * time.Second, MaxStdout: 1024, MaxStderr: 2048})
	if e != nil || r.ExitCode != 0 || !r.OutputExceeded || r.TimedOut || r.Canceled || len(r.Stdout) != 0 || len(r.Stderr) != 0 {
		t.Fatalf("bounded drain contract: %+v %v", r, e)
	}
	for path, want := range map[string][]byte{out.Name(): bytes.Repeat([]byte("o"), 1024), errout.Name(): bytes.Repeat([]byte("e"), 2048)} {
		got, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(got, want) {
			t.Fatalf("bounded disk capture: %s len=%d %v", path, len(got), e)
		}
	}
}
