package gitrepo

import (
	"path/filepath"
	"runtime"
	"strings"
)

func Executable() string {
	if runtime.GOOS == "windows" {
		return "git.exe"
	}
	return "git"
}

func identityPath(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}
