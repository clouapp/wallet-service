package e2e

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	testGrace         = 1500 * time.Millisecond
	sleeperLifetime   = "30"
	shellPath         = "/bin/sh"
	sleepPath         = "/bin/sleep"
	ignoreTermAndExec = `trap "" TERM; exec "$0" "$1"`
)

func copyExecutable(t *testing.T, source, destination string) {
	t.Helper()
	content, err := os.ReadFile(source)
	if err != nil {
		t.Skipf("%s unavailable: %v", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, content, 0o700); err != nil {
		t.Fatal(err)
	}
}

// startFakeAPI starts the state-dir "binary" (a copy of sleep) as the running e2e API.
func startFakeAPI(t *testing.T, paths Paths, ignoreSIGTERM bool) *exec.Cmd {
	t.Helper()
	copyExecutable(t, sleepPath, paths.APIBinary)
	command := exec.Command(paths.APIBinary, sleeperLifetime)
	if ignoreSIGTERM {
		command = exec.Command(shellPath, "-c", ignoreTermAndExec, paths.APIBinary, sleeperLifetime)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	waitForExecutable(t, command.Process.Pid, paths.APIBinary)
	if err := WritePrivateFile(paths.APIPIDFile, []byte(strconv.Itoa(command.Process.Pid)+"\n")); err != nil {
		t.Fatal(err)
	}
	go func() { _ = command.Wait() }()
	return command
}

func waitForExecutable(t *testing.T, pid int, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if executable, alive := ProcessExecutable(pid); alive && executable == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("PID %d never ran %s", pid, want)
}

func newTestManager(t *testing.T, paths Paths, healthAnswers bool) (APIManager, *bytes.Buffer, *[]syscall.Signal) {
	t.Helper()
	out := &bytes.Buffer{}
	signals := &[]syscall.Signal{}
	manager := APIManager{
		Paths:     paths,
		Recording: RecordingLock{Path: paths.RecordingLock, Now: time.Now},
		Health: func(context.Context) (int, bool) {
			if healthAnswers {
				return httpOK, true
			}
			return 0, false
		},
		Executable: ProcessExecutable,
		Signal: func(pid int, signal syscall.Signal) error {
			*signals = append(*signals, signal)
			return syscall.Kill(pid, signal)
		},
		BuildBinary:   func(context.Context, string) error { t.Fatal("unexpected build"); return nil },
		Sleep:         func(ctx context.Context, d time.Duration) error { return SleepContext(ctx, d/10) },
		Now:           time.Now,
		SIGTERMGrace:  testGrace,
		HealthTimeout: time.Second,
		Out:           out,
	}
	return manager, out, signals
}

func TestAPIStopUsesSIGTERMWhenTheProcessHonoursIt(t *testing.T) {
	paths := newTestPaths(t)
	fake := startFakeAPI(t, paths, false)
	manager, out, signals := newTestManager(t, paths, true)
	if err := manager.Run(context.Background(), APIOptions{NoStart: true, LockWait: time.Second}); err != nil {
		t.Fatal(err)
	}
	if len(*signals) != 1 || (*signals)[0] != syscall.SIGTERM {
		t.Fatalf("signals %v", *signals)
	}
	pid := fake.Process.Pid
	if !strings.Contains(out.String(), "stopping PID "+strconv.Itoa(pid)+" (SIGTERM)\nPID "+strconv.Itoa(pid)+" exited\n") {
		t.Fatalf("output %s", out.String())
	}
}

func TestAPIStopEscalatesToSIGKILLAfterTheGracePeriod(t *testing.T) {
	paths := newTestPaths(t)
	fake := startFakeAPI(t, paths, true)
	manager, out, signals := newTestManager(t, paths, true)
	started := time.Now()
	if err := manager.Run(context.Background(), APIOptions{NoStart: true, LockWait: time.Second}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < testGrace {
		t.Fatalf("SIGKILL after %s, before the %s grace", elapsed, testGrace)
	}
	if len(*signals) != 2 || (*signals)[0] != syscall.SIGTERM || (*signals)[1] != syscall.SIGKILL {
		t.Fatalf("signals %v", *signals)
	}
	if _, alive := ProcessExecutable(fake.Process.Pid); alive {
		t.Fatal("process survived SIGKILL")
	}
	if !strings.Contains(out.String(), "ignored SIGTERM for 1s; SIGKILL") {
		t.Fatalf("output %s", out.String())
	}
}

func TestAPIRefusesForeignProcessesRecordingsAndBusyPorts(t *testing.T) {
	paths := newTestPaths(t)
	foreign := exec.Command(sleepPath, sleeperLifetime)
	if err := foreign.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = foreign.Process.Kill(); _ = foreign.Wait() })
	if err := WritePrivateFile(paths.APIPIDFile, []byte(strconv.Itoa(foreign.Process.Pid)+"\n")); err != nil {
		t.Fatal(err)
	}
	manager, _, signals := newTestManager(t, paths, false)
	if err := manager.Run(context.Background(), APIOptions{NoStart: true, LockWait: time.Second}); err == nil || !strings.Contains(err.Error(), "not touching it") {
		t.Fatalf("foreign PID: %v", err)
	}
	if len(*signals) != 0 {
		t.Fatal("signalled a foreign process")
	}

	if err := os.WriteFile(paths.RecordingLock, []byte(`{"owner": "markets-recording"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Run(context.Background(), APIOptions{LockWait: time.Second}); err == nil || !strings.Contains(err.Error(), "during a recording") {
		t.Fatalf("recording: %v", err)
	}
	if err := os.Remove(paths.RecordingLock); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(paths.APIPIDFile); err != nil {
		t.Fatal(err)
	}
	copyExecutable(t, sleepPath, paths.APIBinary)
	busy, _, _ := newTestManager(t, paths, true)
	if err := busy.Run(context.Background(), APIOptions{LockWait: time.Second}); err == nil || !strings.Contains(err.Error(), "already answers") {
		t.Fatalf("busy port: %v", err)
	}

	held, err := AcquireFileLock(context.Background(), paths.RestartLock, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	if err := busy.Run(context.Background(), APIOptions{LockWait: 300 * time.Millisecond}); err == nil || !strings.Contains(err.Error(), "held by another process") {
		t.Fatalf("restart lock: %v", err)
	}
}

func TestAPIStartRunsTheBinaryWithTheEnvironAndWaitsForHealth(t *testing.T) {
	paths := newTestPaths(t)
	script := "#!" + shellPath + "\necho \"db=$DB_DATABASE cwd=$(pwd)\"\nexec " + sleepPath + " " + sleeperLifetime + "\n"
	if err := os.MkdirAll(filepath.Dir(paths.APIBinary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.APIBinary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateFile(paths.APIEnviron, EncodeEnviron(map[string]string{"DB_DATABASE": E2EDatabase})); err != nil {
		t.Fatal(err)
	}
	manager, out, _ := newTestManager(t, paths, false)
	healthChecks := 0
	manager.Health = func(context.Context) (int, bool) {
		healthChecks++
		if healthChecks == 1 {
			return 0, false
		}
		return httpOK, true
	}
	pid, err := manager.start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if !strings.Contains(out.String(), "started PID "+strconv.Itoa(pid)+"; health 200") {
		t.Fatalf("output %s", out.String())
	}
	requireMode(t, paths.APIPIDFile, PrivateFileMode)
	requireMode(t, paths.APILog, PrivateFileMode)
	if content, _ := os.ReadFile(paths.APIPIDFile); string(content) != strconv.Itoa(pid)+"\n" {
		t.Fatalf("pid file %q", content)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if log, _ := os.ReadFile(paths.APILog); strings.Contains(string(log), "db=vault_test cwd="+paths.BackDir) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	log, _ := os.ReadFile(paths.APILog)
	t.Fatalf("log %q", log)
}

func TestAPIStartReportsAnEarlyExit(t *testing.T) {
	paths := newTestPaths(t)
	if err := os.MkdirAll(filepath.Dir(paths.APIBinary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.APIBinary, []byte("#!"+shellPath+"\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateFile(paths.APIEnviron, EncodeEnviron(map[string]string{"A": "1"})); err != nil {
		t.Fatal(err)
	}
	manager, _, _ := newTestManager(t, paths, false)
	manager.HealthTimeout = 5 * time.Second
	if _, err := manager.start(context.Background()); err == nil || !strings.Contains(err.Error(), "API exited with 7") {
		t.Fatalf("early exit: %v", err)
	}
}

func TestAPIBuildKeepsThePreviousBinary(t *testing.T) {
	paths := newTestPaths(t)
	copyExecutable(t, sleepPath, paths.APIBinary)
	manager, out, _ := newTestManager(t, paths, false)
	manager.Now = clock(fixedNow)
	manager.BuildBinary = func(_ context.Context, output string) error {
		return os.WriteFile(output, []byte("fresh"), 0o700)
	}
	if err := manager.build(context.Background()); err != nil {
		t.Fatal(err)
	}
	previous := paths.APIBinary + ".prev-20261003T050000Z"
	if content, _ := os.ReadFile(paths.APIBinary); string(content) != "fresh" {
		t.Fatal("fresh binary not installed")
	}
	requireMode(t, previous, 0o700)
	if !strings.Contains(out.String(), "previous binary kept as "+previous) {
		t.Fatalf("output %s", out.String())
	}
}

func TestAPIStatusReportsWithoutChangingAnything(t *testing.T) {
	paths := newTestPaths(t)
	manager, out, signals := newTestManager(t, paths, true)
	if err := WritePrivateFile(paths.RecordingLock, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := manager.Run(context.Background(), APIOptions{Status: true}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "pid=- binary="+paths.APIBinary+" health=200 log="+paths.APILog+"\n" || len(*signals) != 0 {
		t.Fatalf("status %q", out.String())
	}
}
