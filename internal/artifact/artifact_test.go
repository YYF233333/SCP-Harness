package artifact

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"scp-harness/internal/config"
	"scp-harness/internal/model"
)

func limits() config.Limits {
	return config.Limits{Files: 10, Bytes: 1 << 20, Single: 1 << 19, Path: 100}
}
func tarBytes(t *testing.T, headers []*tar.Header, contents [][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for i, h := range headers {
		if e := w.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if len(contents) > i {
			if _, e := w.Write(contents[i]); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestCaptureBoundsAtomicityAndIntegrity(t *testing.T) {
	l := limits()
	payload := bytes.Repeat([]byte("x"), 1<<18)
	data := tarBytes(t, []*tar.Header{{Name: "large.bin", Mode: 0755, Size: int64(len(payload)), Typeflag: tar.TypeReg}}, [][]byte{payload})
	root := t.TempDir()
	source := filepath.Join(root, "source.tar")
	if e := os.WriteFile(source, data, 0600); e != nil {
		t.Fatal(e)
	}
	store := filepath.Join(root, "store")
	if e := os.Mkdir(store, 0700); e != nil {
		t.Fatal(e)
	}
	a, e := Publish(store, model.Artifact{ID: model.ID()}, source, l)
	if e != nil {
		t.Fatal(e)
	}
	if a.SHA256 == "" || a.Size != int64(len(data)) {
		t.Fatal("capture metadata")
	}
	dst := t.TempDir()
	if e = Restore(a, dst, l); e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(filepath.Join(dst, "large.bin"))
	if e != nil || !bytes.Equal(got, payload) {
		t.Fatal("restore bytes")
	}
	f, e := os.Open(a.BlobPath)
	if e != nil {
		t.Fatal(e)
	}
	h, e := tar.NewReader(f).Next()
	f.Close()
	if e != nil || h.Mode != 0755 {
		t.Fatal("capture lost executable mode")
	}
	if e = os.WriteFile(a.BlobPath, []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = Restore(a, t.TempDir(), l); model.Code(e) != "CORE_INCONSISTENT" {
		t.Fatal("tampered artifact accepted")
	}
	for _, tc := range []struct {
		name    string
		headers []*tar.Header
		body    [][]byte
		limits  config.Limits
	}{
		{"traversal", []*tar.Header{{Name: "../outside", Typeflag: tar.TypeReg}}, nil, l},
		{"symlink", []*tar.Header{{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "file"}}, nil, l},
		{"hardlink", []*tar.Header{{Name: "link", Typeflag: tar.TypeLink, Linkname: "file"}}, nil, l},
		{"fifo", []*tar.Header{{Name: "pipe", Typeflag: tar.TypeFifo}}, nil, l},
		{"duplicate", []*tar.Header{{Name: "file", Typeflag: tar.TypeReg}, {Name: "FILE", Typeflag: tar.TypeReg}}, nil, l},
		{"file_count", []*tar.Header{{Name: "a", Typeflag: tar.TypeReg}, {Name: "b", Typeflag: tar.TypeReg}}, nil, config.Limits{Files: 1, Bytes: 100, Single: 100, Path: 100}},
		{"total_bytes", []*tar.Header{{Name: "a", Typeflag: tar.TypeReg, Size: 2}, {Name: "b", Typeflag: tar.TypeReg, Size: 2}}, [][]byte{[]byte("aa"), []byte("bb")}, config.Limits{Files: 10, Bytes: 3, Single: 3, Path: 100}},
		{"single_file", []*tar.Header{{Name: "a", Typeflag: tar.TypeReg, Size: 4}}, [][]byte{[]byte("aaaa")}, config.Limits{Files: 10, Bytes: 10, Single: 3, Path: 100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := tarBytes(t, tc.headers, tc.body)
			if e := ValidateArchive(bytes.NewReader(bad), tc.limits); model.Code(e) != "LIMIT_EXCEEDED" {
				t.Fatalf("invalid capture accepted: %v", e)
			}
			if e := Extract(bytes.NewReader(bad), t.TempDir(), tc.limits); model.Code(e) != "LIMIT_EXCEEDED" {
				t.Fatalf("invalid extraction accepted: %v", e)
			}
		})
	}
	broken := filepath.Join(root, "broken.tar")
	if e = os.WriteFile(broken, data[:600], 0600); e != nil {
		t.Fatal(e)
	}
	id := model.ID()
	if _, e = Publish(store, model.Artifact{ID: id}, broken, l); model.Code(e) != "LIMIT_EXCEEDED" {
		t.Fatal("partial capture accepted")
	}
	if _, e = os.Stat(filepath.Join(store, id, "workspace.tar")); !os.IsNotExist(e) {
		t.Fatal("partial artifact published")
	}
	if _, e = Publish(source, model.Artifact{ID: model.ID()}, source, l); model.Code(e) != "STORAGE_FAILURE" {
		t.Fatal("durable path failure misclassified")
	}
}
