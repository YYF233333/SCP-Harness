package artifact

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"scp-harness/internal/config"
	"scp-harness/internal/model"
)

func SafeName(name string, max int64) (string, error) {
	name = strings.TrimSuffix(name, "/")
	if int64(len(name)) > max || name == "" || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
		return "", model.Err("LIMIT_EXCEEDED", "unsafe archive path %q", name)
	}
	for _, p := range strings.Split(name, "/") {
		stem := strings.ToUpper(strings.SplitN(p, ".", 2)[0])
		if strings.EqualFold(p, ".git") || strings.HasSuffix(p, ".") || strings.HasSuffix(p, " ") || stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9' {
			return "", model.Err("LIMIT_EXCEEDED", "unsupported archive path %q", name)
		}
	}
	return name, nil
}

// Extract accepts only regular files/directories and never follows links. The
// destination must be a new, empty directory owned by this operation.
func Extract(r io.Reader, root string, l config.Limits) (result error) {
	defer func() {
		if result != nil && model.Code(result) == "INTERNAL_ERROR" {
			result = StorageError("extract workspace", result)
		}
	}()
	entries, e := os.ReadDir(root)
	if e != nil {
		return e
	}
	if len(entries) != 0 {
		return model.Err("PRECONDITION_FAILED", "extraction requires empty destination")
	}
	tr := tar.NewReader(r)
	var count, total int64
	seen := map[string]bool{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return model.Err("LIMIT_EXCEEDED", "invalid tar: %v", e)
		}
		// Git archive emits a global PAX metadata header containing the commit
		// identity. It does not materialize a filesystem entry.
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && h.Typeflag != tar.TypeDir {
			return model.Err("LIMIT_EXCEEDED", "unsupported tar entry %s", h.Name)
		}
		name, e := SafeName(h.Name, l.Path)
		if e != nil {
			return e
		}
		key := strings.ToLower(name)
		if seen[key] {
			return model.Err("LIMIT_EXCEEDED", "duplicate/colliding archive path")
		}
		seen[key] = true
		count++
		if count > l.Files || h.Size < 0 || h.Size > l.Single || h.Size > l.Bytes-total {
			return model.Err("LIMIT_EXCEEDED", "archive exceeds workspace bounds")
		}
		total += h.Size
		dest := filepath.Join(root, filepath.FromSlash(name))
		if h.Typeflag == tar.TypeDir {
			if e = os.MkdirAll(dest, 0700); e != nil {
				return e
			}
			continue
		}
		if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
			return e
		}
		f, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600|os.FileMode(h.Mode)&0111)
		if e != nil {
			return e
		}
		_, e = io.CopyN(f, tr, h.Size)
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	}
}
func Pack(root string, w io.Writer, l config.Limits) error {
	tw := tar.NewWriter(w)
	var count, total int64
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p == root {
			return nil
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if strings.EqualFold(rel, ".git") && d.IsDir() {
			return filepath.SkipDir
		}
		if _, e = SafeName(rel, l.Path); e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return model.Err("LIMIT_EXCEEDED", "unsupported file %s", rel)
		}
		count++
		if count > l.Files || info.Size() > l.Single {
			return model.Err("LIMIT_EXCEEDED", "workspace file limit")
		}
		if !info.IsDir() {
			if info.Size() > l.Bytes-total {
				return model.Err("LIMIT_EXCEEDED", "workspace byte limit")
			}
			total += info.Size()
		}
		h, e := tar.FileInfoHeader(info, "")
		if e != nil {
			return e
		}
		h.Name = rel
		h.Uid = 0
		h.Gid = 0
		h.Uname = ""
		h.Gname = ""
		if e = tw.WriteHeader(h); e != nil {
			return e
		}
		if info.IsDir() {
			return nil
		}
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		_, e = io.CopyN(tw, f, info.Size())
		ce := f.Close()
		if e != nil {
			return e
		}
		return ce
	})
	if e != nil {
		_ = tw.Close()
		return e
	}
	return tw.Close()
}
func ValidateArchive(r io.Reader, l config.Limits) error {
	tr := tar.NewReader(r)
	var count, total int64
	seen := map[string]bool{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return model.Err("LIMIT_EXCEEDED", "invalid capture tar: %v", e)
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return model.Err("LIMIT_EXCEEDED", "unsupported capture entry")
		}
		name, e := SafeName(h.Name, l.Path)
		if e != nil {
			return e
		}
		key := strings.ToLower(name)
		if seen[key] {
			return model.Err("LIMIT_EXCEEDED", "duplicate capture path")
		}
		seen[key] = true
		count++
		if count > l.Files || h.Size < 0 || h.Size > l.Single || h.Size > l.Bytes-total {
			return model.Err("LIMIT_EXCEEDED", "capture exceeds file bounds")
		}
		total += h.Size
		if _, e = io.CopyN(io.Discard, tr, h.Size); e != nil {
			return model.Err("LIMIT_EXCEEDED", "truncated capture")
		}
	}
}

// Publish retains the captured tar's file modes; round-tripping through NTFS
// before publication would lose Unix executable bits.
func Publish(store string, a model.Artifact, source string, l config.Limits) (model.Artifact, error) {
	src, e := os.Open(source)
	if e != nil {
		return a, StorageError("capture source", e)
	}
	defer src.Close()
	info, e := src.Stat()
	if e != nil {
		return a, StorageError("capture stat", e)
	}
	if info.Size() > MaxTar(l) {
		return a, model.Err("LIMIT_EXCEEDED", "capture tar too large")
	}
	if e = ValidateArchive(src, l); e != nil {
		return a, e
	}
	if _, e = src.Seek(0, 0); e != nil {
		return a, StorageError("capture rewind", e)
	}
	dir := filepath.Join(store, a.ID)
	if e := os.Mkdir(dir, 0700); e != nil {
		return a, model.Err("STORAGE_FAILURE", "artifact directory: %v", e)
	}
	tmp := filepath.Join(dir, "workspace.tar.tmp")
	f, e := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return a, model.Err("STORAGE_FAILURE", "artifact temp: %v", e)
	}
	h := sha256.New()
	_, e = io.Copy(io.MultiWriter(f, h), io.LimitReader(src, MaxTar(l)))
	if e == nil {
		e = f.Sync()
	}
	info, se := f.Stat()
	ce := f.Close()
	if e != nil {
		if model.Code(e) == "LIMIT_EXCEEDED" {
			return a, e
		}
		return a, model.Err("STORAGE_FAILURE", "artifact capture: %v", e)
	}
	if se != nil || ce != nil {
		return a, model.Err("STORAGE_FAILURE", "artifact flush/stat: %v/%v", se, ce)
	}
	a.SHA256 = hex.EncodeToString(h.Sum(nil))
	a.Size = info.Size()
	a.BlobPath = filepath.Join(dir, "workspace.tar")
	if e = os.Rename(tmp, a.BlobPath); e != nil {
		return a, model.Err("STORAGE_FAILURE", "artifact publication: %v", e)
	}
	meta := map[string]any{"sha256": a.SHA256, "size_bytes": a.Size, "source_attempt": a.AttemptID, "semantic_anchor_option": a.Anchor, "base_repo_sha": a.BaseSHA}
	b, _ := json.Marshal(meta)
	mf, e := os.OpenFile(filepath.Join(dir, "meta.json.tmp"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return a, model.Err("STORAGE_FAILURE", "artifact metadata: %v", e)
	}
	_, e = mf.Write(b)
	if e == nil {
		e = mf.Sync()
	}
	ce = mf.Close()
	if e != nil || ce != nil {
		return a, model.Err("STORAGE_FAILURE", "artifact metadata flush")
	}
	if e = os.Rename(filepath.Join(dir, "meta.json.tmp"), filepath.Join(dir, "meta.json")); e != nil {
		return a, model.Err("STORAGE_FAILURE", "artifact metadata publication: %v", e)
	}
	return a, nil
}
func Restore(a model.Artifact, root string, l config.Limits) error {
	f, e := os.Open(a.BlobPath)
	if e != nil {
		return model.Err("STORAGE_FAILURE", "read artifact: %v", e)
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, a.Size+1))
	if e != nil || n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return model.Err("CORE_INCONSISTENT", "Artifact digest/size mismatch")
	}
	if _, e = f.Seek(0, 0); e != nil {
		return e
	}
	return Extract(f, root, l)
}
func Export(a model.Artifact, out string) error {
	src, e := os.Open(a.BlobPath)
	if e != nil {
		return model.Err("STORAGE_FAILURE", "artifact read: %v", e)
	}
	defer src.Close()
	dst, e := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return model.Err("PRECONDITION_FAILED", "export destination: %v", e)
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(dst, h), io.LimitReader(src, a.Size+1))
	if e == nil {
		e = dst.Sync()
	}
	ce := dst.Close()
	if e != nil || ce != nil {
		return model.Err("STORAGE_FAILURE", "export: %v/%v", e, ce)
	}
	if n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return model.Err("CORE_INCONSISTENT", "export digest mismatch")
	}
	return nil
}
func MaxTar(l config.Limits) int64 {
	if l.Bytes > 1<<63-1-10240 || l.Files > (1<<63-1-l.Bytes-10240)/4096 {
		return 1<<63 - 1
	}
	return l.Bytes + l.Files*4096 + 10240
}
func Copy(root, destination string, l config.Limits) error {
	f, e := os.CreateTemp(filepath.Dir(destination), "copy-*.tar")
	if e != nil {
		return StorageError("snapshot copy", e)
	}
	defer f.Close()
	defer os.Remove(f.Name())
	if e = Pack(root, f, l); e != nil {
		return e
	}
	if _, e = f.Seek(0, 0); e != nil {
		return StorageError("snapshot copy seek", e)
	}
	return Extract(f, destination, l)
}
func StorageError(op string, e error) error {
	if e == nil {
		return nil
	}
	if model.Code(e) != "INTERNAL_ERROR" {
		return e
	}
	return model.Err("STORAGE_FAILURE", "%s: %s", op, fmt.Sprint(e))
}
