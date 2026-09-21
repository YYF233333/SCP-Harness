package artifact

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestR3HostTransientCleanupProgress(t *testing.T) {
	for _, kind := range []string{"oversized_file", "many_files", "long_path"} {
		t.Run(kind, func(t *testing.T) {
			l := limits()
			l.Files = 2
			l.Single = 8
			l.Bytes = 16
			l.Path = 12
			l.ProcessMS = 10000
			original := l
			root := filepath.Join(t.TempDir(), "workspace")
			if e := os.Mkdir(root, 0700); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "oversized_file":
				f, e := os.Create(filepath.Join(root, "large"))
				if e != nil {
					t.Fatal(e)
				}
				if e = f.Truncate(1024); e != nil {
					t.Fatal(e)
				}
				f.Close()
			case "many_files":
				for i := 0; i < 25; i++ {
					if e := os.WriteFile(filepath.Join(root, fmt.Sprint(i)), nil, 0600); e != nil {
						t.Fatal(e)
					}
				}
			case "long_path":
				deep := filepath.Join(root, strings.Repeat("a", 40), strings.Repeat("b", 40))
				if e := os.MkdirAll(deep, 0700); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(filepath.Join(deep, "leaf"), nil, 0600); e != nil {
					t.Fatal(e)
				}
			}
			before := countTransient(t, root)
			done, e := discardBatch(root, 1, time.Now().Add(10*time.Second))
			if e != nil {
				t.Fatal(e)
			}
			after := countTransient(t, root)
			if done || after != before-1 {
				t.Fatalf("bounded first batch made no deletion progress: before=%d after=%d done=%t", before, after, done)
			}
			if e = Discard(root, l); e != nil {
				t.Fatal(e)
			}
			if _, e = os.Stat(root); !os.IsNotExist(e) {
				t.Fatal("cleanup left its transient root")
			}
			if l != original {
				t.Fatal("cleanup relaxed configured acceptance limits")
			}
		})
	}
}

func countTransient(t *testing.T, root string) int {
	t.Helper()
	n := 0
	if e := filepath.WalkDir(root, func(_ string, _ os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		n++
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	return n
}
