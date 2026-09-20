//go:build !windows

package boundedexec

import "os"

// Production execution is Windows-only; portable unit tests have no descendants.
func contain(p *os.Process) (func(), error) { return func() { _ = p.Kill() }, nil }
