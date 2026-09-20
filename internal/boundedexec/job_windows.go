//go:build windows

package boundedexec

import (
	"os"
	"syscall"
	"unsafe"
)

var kernel = syscall.NewLazyDLL("kernel32.dll")
var createJob = kernel.NewProc("CreateJobObjectW")
var setJob = kernel.NewProc("SetInformationJobObject")
var assignJob = kernel.NewProc("AssignProcessToJobObject")

type basicLimit struct {
	ProcessTime, JobTime   int64
	Flags                  uint32
	MinWorking, MaxWorking uintptr
	ActiveProcess          uint32
	Affinity               uintptr
	Priority, Scheduling   uint32
}
type ioCounters struct{ ReadOps, WriteOps, OtherOps, ReadBytes, WriteBytes, OtherBytes uint64 }
type jobLimit struct {
	Basic                                                      basicLimit
	IO                                                         ioCounters
	ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory uintptr
}

func contain(p *os.Process) (func(), error) {
	h, _, e := createJob.Call(0, 0)
	if h == 0 {
		return func() {}, e
	}
	closeJob := func() { _ = syscall.CloseHandle(syscall.Handle(h)) }
	info := jobLimit{}
	info.Basic.Flags = 0x2000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	ok, _, e := setJob.Call(h, 9, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if ok == 0 {
		closeJob()
		return func() {}, e
	}
	ph, e := syscall.OpenProcess(0x0001|0x0100, false, uint32(p.Pid))
	if e != nil {
		closeJob()
		return func() {}, e
	}
	defer syscall.CloseHandle(ph)
	ok, _, e = assignJob.Call(h, uintptr(ph))
	if ok == 0 {
		closeJob()
		return func() {}, e
	}
	return closeJob, nil
}
