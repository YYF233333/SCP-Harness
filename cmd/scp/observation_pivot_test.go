//go:build linux || (windows && release)

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"scp-harness/internal/boundedexec"
	"scp-harness/internal/scheduler"
)

func TestObservationDirectorySymlinkPivot(t *testing.T) {
	// The real worker only renames its directory and creates/unlinks the symlink.
	// It never opens the external target, so external inotify events implicate
	// traversal even if a later identity check prevents leaking tar contents.
	script := `exec python3 -u -c 'import os,time
root=os.path.dirname(os.environ["SCP_INPUT"])
os.mkdir("pivot")
with open("pivot/inside.txt","w") as f: f.write("inside only\n")
for i in range(1500):
 with open("padding-%04d"%i,"w") as f: f.write("unchanged\n")
print("PIVOT_READY",flush=True)
i=0
while not os.path.exists(root+"/stop-pivot"):
 os.rename("pivot","parked")
 os.symlink(root+"/outside-pivot","pivot")
 time.sleep(0.002)
 os.unlink("pivot")
 os.rename("parked","pivot")
 with open("pivot/inside.txt") as f: assert f.read()=="inside only\n"
 i+=1
 if i%100==0: print("PIVOT_TICK",flush=True)
 time.sleep(0.002)
print("PIVOT_STOPPED",flush=True)
while not os.path.exists(root+"/finish-pivot"): time.sleep(0.01)
with open(os.environ["SCP_RESULT"],"w") as f:
 f.write("{\"schema_version\":0,\"operation\":\"mutation\",\"disposition\":\"DROP_FINAL\",\"claims\":[],\"new_options\":[]}")
'`
	c, configPath, exe, _ := observationFixture(t, script)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	joined := false
	go func() { _, e := scheduler.New(c).Step(ctx); done <- e }()
	t.Cleanup(func() {
		cancel()
		if !joined {
			<-done
		}
	})
	id, stdout := "", ""
	waitObservation(t, func() bool {
		s, e := c.Store.Read()
		if e != nil {
			t.Fatal(e)
		}
		for _, a := range s.Attempts {
			b, _ := os.ReadFile(a.Stdout)
			if a.Status == "RUNNING" && bytes.Contains(b, []byte("PIVOT_READY")) {
				id, stdout = a.ID, a.Stdout
				return true
			}
		}
		return false
	})
	markerName, markerContent := "EXTERNAL_MARKER_"+id, "external-secret-"+id
	// This root-owned test probe watches the external directory and marker inode.
	// Positive controls afterward prove the kernel watch can detect both directory
	// enumeration and marker reads. It does not intercept or replace diff/helper IO.
	monitor := `import ctypes,json,os,sys,time
root,name,content=sys.argv[1:]
outside=root+"/outside-pivot"
os.mkdir(outside,0o700)
with open(outside+"/"+name,"w") as f: f.write(content)
lib=ctypes.CDLL(None,use_errno=True)
lib.inotify_init1.argtypes=[ctypes.c_int]
lib.inotify_add_watch.argtypes=[ctypes.c_int,ctypes.c_char_p,ctypes.c_uint32]
fd=lib.inotify_init1(os.O_NONBLOCK|os.O_CLOEXEC)
assert fd>=0,ctypes.get_errno()
for p in (outside,outside+"/"+name):
 assert lib.inotify_add_watch(fd,os.fsencode(p),0x00000001|0x00000020)>=0,ctypes.get_errno()
def drain():
 total=0
 while True:
  try: total+=len(os.read(fd,65536))
  except BlockingIOError: return total
print("ARMED",flush=True)
end=time.monotonic()+50
while not os.path.exists(root+"/stop-monitor"):
 assert time.monotonic()<end,"monitor deadline"
 time.sleep(0.01)
events=drain()
with os.scandir(outside) as entries: assert [e.name for e in entries]==[name]
directory_probe=drain()
with open(outside+"/"+name) as f: assert f.read()==content
file_probe=drain()
os.close(fd)
print(json.dumps(dict(events=events,directory_probe=directory_probe,file_probe=file_probe)),flush=True)
`
	var monitorOutput observationOutput
	monitorDone := make(chan error, 1)
	monitorCtx, stopMonitor := context.WithCancel(context.Background())
	monitorJoined := false
	go func() {
		r, e := c.Runner.Control(monitorCtx, []string{"python3", "-u", "-c", monitor, c.Runner.Root(), markerName, markerContent}, nil, &monitorOutput, 4096)
		if e == nil && r.ExitCode != 0 {
			e = fmt.Errorf("outside monitor: exit %d: %s", r.ExitCode, r.Stderr)
		}
		monitorDone <- e
	}()
	t.Cleanup(func() {
		stopMonitor()
		if !monitorJoined {
			<-monitorDone
		}
	})
	waitObservation(t, func() bool { return strings.Contains(monitorOutput.text(), "ARMED") })
	before := observationState(t, c)
	// Exclude only the two names the worker is moving. All other workspace bytes
	// and the entire synthetic .git remain covered. The worker checks inside.txt
	// after every replacement; the final stable capture checks it again.
	fingerprint := func() string {
		r, e := c.Runner.Control(context.Background(), []string{"python3", "-c", `import hashlib,os,sys
h=hashlib.sha256()
def visit(base,prefix=""):
 with os.scandir(base) as entries:
  for entry in sorted(entries,key=lambda e:e.name):
   if not prefix and entry.name in ("pivot","parked"): continue
   name=prefix+entry.name
   if entry.is_dir(follow_symlinks=False): visit(entry.path,name+"/")
   else:
    assert entry.is_file(follow_symlinks=False)
    h.update(name.encode()); h.update(open(entry.path,"rb").read())
visit(sys.argv[1])
print(h.hexdigest())`, c.Runner.Root() + "/workspace"}, nil, nil, 4096)
		if e != nil || r.ExitCode != 0 {
			t.Fatalf("workspace fingerprint: %v %s", e, r.Stderr)
		}
		return string(r.Stdout)
	}
	workspace := fingerprint()
	blocked, snapshots := 0, 0
	for i := 0; i < 32; i++ {
		r, e := boundedexec.Run(context.Background(), boundedexec.Command{Argv: []string{exe, "--config", configPath, "--json", "attempt", "diff", id}, Timeout: 10 * time.Second, MaxStdout: 1 << 20, MaxStderr: 1 << 20})
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(r.Stdout, []byte(markerName)) || bytes.Contains(r.Stdout, []byte(markerContent)) || bytes.Contains(r.Stderr, []byte(markerName)) || bytes.Contains(r.Stderr, []byte(markerContent)) {
			t.Fatal("external marker leaked")
		}
		var envelope struct {
			OK    bool
			Data  struct{ Text string }
			Error struct{ Code string }
		}
		if e = json.Unmarshal(r.Stdout, &envelope); e != nil {
			t.Fatalf("diff: %v %s %s", e, r.Stdout, r.Stderr)
		}
		if r.ExitCode == 4 && !envelope.OK && envelope.Error.Code == "BLOCKED" {
			blocked++
			continue
		}
		if r.ExitCode != 0 || !envelope.OK {
			t.Fatalf("unexpected observation result: %d %s %s", r.ExitCode, r.Stdout, r.Stderr)
		}
		if !strings.Contains(envelope.Data.Text, "inside only") {
			t.Fatal("snapshot lost internal payload")
		}
		snapshots++
	}
	if !bytes.Equal(before, observationState(t, c)) || workspace != fingerprint() {
		t.Fatal("observation changed Core state/ledger, workspace or .git")
	}
	signal := func(name string) {
		r, e := c.Runner.Control(context.Background(), []string{"touch", c.Runner.Root() + "/" + name}, nil, nil, 4096)
		if e != nil || r.ExitCode != 0 {
			t.Fatalf("test barrier: %v %s", e, r.Stderr)
		}
	}
	signal("stop-monitor")
	if e := <-monitorDone; e != nil {
		monitorJoined = true
		t.Fatal(e)
	}
	monitorJoined = true
	var evidence struct {
		Events    int `json:"events"`
		Directory int `json:"directory_probe"`
		File      int `json:"file_probe"`
	}
	if e := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(monitorOutput.text(), "ARMED\n"))), &evidence); e != nil {
		t.Fatal(e, monitorOutput.text())
	}
	if evidence.Events != 0 || evidence.Directory == 0 || evidence.File == 0 {
		t.Fatalf("external directory/marker accessed or monitor ineffective: %+v", evidence)
	}
	signal("stop-pivot")
	waitObservation(t, func() bool { b, _ := os.ReadFile(stdout); return bytes.Contains(b, []byte("PIVOT_STOPPED")) })
	b, _ := os.ReadFile(stdout)
	if !bytes.Contains(b, []byte("PIVOT_TICK")) {
		t.Fatal("worker did not continue replacing directories")
	}
	diff, e := c.DiffAttempt(context.Background(), id)
	if e != nil || !strings.Contains(diff.Text, "A pivot/inside.txt\n") || !strings.Contains(diff.Text, "inside only") || strings.Contains(diff.Text, "parked") || strings.Contains(diff.Text, markerName) {
		t.Fatalf("stable inside snapshot: %+v %v", diff, e)
	}
	if !bytes.Equal(before, observationState(t, c)) || workspace != fingerprint() {
		t.Fatal("observation altered execution/authority or workspace bytes")
	}
	signal("finish-pivot")
	if e = <-done; e != nil {
		joined = true
		t.Fatal(e)
	}
	joined = true
	t.Logf("RUNNING pivot: %d BLOCKED retries, %d consistent snapshots; external open/access events=0 (both positive controls detected); marker absent; workspace/.git/state/ledger unchanged; worker continued and returned", blocked, snapshots)
}
