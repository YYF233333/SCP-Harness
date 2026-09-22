//go:build windows && release

package wsl

import (
	"context"
	"strings"
	"testing"
	"time"

	"scp-harness/internal/config"
	"scp-harness/internal/model"
)

// Uses the same account, mounts and unconditional distro termination as Attempts.
// No model cooperation or provider permission policy is involved.
func TestWorkerRuntimeLifetime(t *testing.T) {
	cfg, e := config.Load("../../scp.example.json")
	if e != nil {
		t.Fatal(e)
	}
	runner := Runner{Config: cfg}
	for _, outcome := range []string{"return", "crash", "timeout", "interrupt"} {
		t.Run(outcome, func(t *testing.T) {
			ctx := context.Background()
			if e := runner.Check(ctx); e != nil {
				t.Fatal(e)
			}
			marker := "scp-state-" + model.ID()
			// Deliberately detach a child from the foreground process group.
			program := `import os,pathlib,subprocess,sys,time
name,kind=sys.argv[1:]
paths=['/home/scp','/tmp','/var/tmp','/dev/shm','/run/shm','/run/lock']
for path in paths:
 p=pathlib.Path(path)/name
 p.write_text(name)
print('MARKERS_READY',flush=True)
subprocess.Popen(['python3','-c','import time; time.sleep(3600)',name],start_new_session=True,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
if kind in ('timeout','interrupt'): time.sleep(3600)
sys.exit(9 if kind=='crash' else 0)
`
			active, cancel := context.WithCancel(ctx)
			defer cancel()
			limit := 15 * time.Second
			if outcome == "timeout" {
				limit = 3 * time.Second
			}
			if outcome == "interrupt" {
				time.AfterFunc(3*time.Second, cancel)
			}
			r, err := runner.Run(active, "scp", []string{"python3", "-c", program, marker, outcome}, nil, nil, limit, 65536, 65536)
			// Even an assertion failure must not leave a detached child alive.
			term := runner.Terminate(ctx)
			if err != nil || term != nil {
				t.Fatal(err, term)
			}
			if !strings.Contains(string(r.Stdout), "MARKERS_READY") {
				t.Fatalf("writer did not execute: %+v", r)
			}
			if outcome == "return" && r.ExitCode != 0 || outcome == "crash" && r.ExitCode != 9 || outcome == "timeout" && !r.TimedOut || outcome == "interrupt" && !r.Canceled {
				t.Fatalf("wrong outcome: %+v", r)
			}
			if e := runner.Check(ctx); e != nil {
				t.Fatal(e)
			}
			probe := `import pathlib,sys
name=sys.argv[1]
for root in ('/home/scp','/tmp','/var/tmp','/dev/shm','/run/shm','/run/lock'):
 assert not (pathlib.Path(root)/name).exists(),root
for proc in pathlib.Path('/proc').iterdir():
 if not proc.name.isdigit(): continue
 try: args=(proc/'cmdline').read_bytes().split(b'\0')
 except (OSError,ProcessLookupError): continue
 assert args[:2]!=[b'python3',b'-c'] or b'import time; time.sleep(3600)' not in args or name.encode() not in args,'detached child survived'
print('NO_STATE_OR_CHILD')
`
			r, err = runner.Run(ctx, "scp", []string{"python3", "-c", probe, marker}, nil, nil, 15*time.Second, 65536, 65536)
			if err != nil || r.ExitCode != 0 {
				t.Fatalf("state survived termination: %v %s", err, r.Stderr)
			}
			t.Logf("%s: markers in six ordinary writable runtime paths and detached child disappeared", outcome)
		})
	}
	if e := runner.Terminate(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestWorkerHostAuthorityDenied(t *testing.T) {
	cfg, e := config.Load("../../scp.example.json")
	if e != nil {
		t.Fatal(e)
	}
	runner := Runner{Config: cfg}
	ctx := context.Background()
	if e := runner.Check(ctx); e != nil {
		t.Fatal(e)
	}
	program := `import os,pathlib,subprocess
assert not os.environ.get('WSL_INTEROP')
assert all(not part.startswith('/mnt/') for part in os.environ['PATH'].split(':'))
assert pathlib.Path('/opt/scp-workers/interop-probe.exe').is_file()
assert os.access('/opt/scp-workers/interop-probe.exe',os.X_OK)
for path in ('/mnt/c','/mnt/wsl','/mnt/wslg','/run/WSL'):
 assert not os.access(path,os.X_OK),path
for force in (False,True):
 env=os.environ.copy()
 if force: env['WSL_INTEROP']='/run/WSL/1_interop'
 try: p=subprocess.run(['/opt/scp-workers/interop-probe.exe','/c','echo','SCP_INTEROP_EXECUTED'],env=env,capture_output=True,timeout=5)
 except OSError: continue
 except subprocess.TimeoutExpired as error:
  assert b'SCP_INTEROP_EXECUTED' not in (error.stdout or b'')
  continue
 assert p.returncode!=0 and b'SCP_INTEROP_EXECUTED' not in p.stdout,(p.returncode,p.stdout)
print('HOST_AUTHORITY_DENIED')
`
	r, e := runner.Run(ctx, "scp", []string{"python3", "-c", program}, nil, nil, 20*time.Second, 65536, 65536)
	if term := runner.Terminate(ctx); term != nil {
		t.Fatal(term)
	}
	if e != nil || r.ExitCode != 0 {
		t.Fatalf("host isolation: %v %s", e, r.Stderr)
	}
	t.Log(string(r.Stdout))
}

func TestWorkerRuntimeAdmission(t *testing.T) {
	cfg, e := config.Load("../../scp.example.json")
	if e != nil {
		t.Fatal(e)
	}
	runner := Runner{Config: cfg}
	ctx := context.Background()
	if e := runner.Check(ctx); e != nil {
		t.Fatal(e)
	}
	path := "/scp/admission-" + model.ID()
	control := func(script string) {
		t.Helper()
		r, e := runner.Control(ctx, []string{"sh", "-c", script, "admission-test", path}, nil, nil, 4096)
		if e != nil || r.ExitCode != 0 {
			t.Fatalf("admission fixture: %v %s", e, r.Stderr)
		}
	}
	control(`mkdir "$1" && touch "$1/state"`)
	t.Cleanup(func() { control(`unlink "$1/state" && rmdir "$1"`) })
	for _, setup := range []string{
		`chown scp:scp "$1" && chmod 000 "$1"`,
		`chown root:root "$1" && chmod 777 "$1"`,
		`chown root:root "$1" && chmod 711 "$1" && chown scp:scp "$1/state"`,
	} {
		control(setup)
		if e := runner.Check(ctx); model.Code(e) != "RUNNER_UNAVAILABLE" {
			t.Fatalf("unsafe persistent runtime admitted: %s: %v", setup, e)
		}
	}
	control(`chown root:root "$1" && chmod 700 "$1"`)
	if e := runner.Check(ctx); e != nil {
		t.Fatal(e)
	}
	t.Log("owner chmod authority, world write and writable state hidden in an unlistable directory all rejected; root-only storage accepted")
}
