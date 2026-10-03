package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	SIGTERMGrace           = 20 * time.Second
	HealthTimeout          = 60 * time.Second
	HealthPollInterval     = 1 * time.Second
	ExitPollInterval       = 1 * time.Second
	KillSettle             = 1 * time.Second
	BuildTimeout           = 600 * time.Second
	DefaultRestartLockWait = 600 * time.Second
	healthRequestTimeout   = 3 * time.Second
	httpOK                 = http.StatusOK
	binaryBackupStamp      = "20060102T150405Z"
	freshBinarySuffix      = ".new"
	deletedExeSuffix       = " (deleted)"
	logFileFlags           = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	goBinary               = "go"
	noValue                = "-"
)

// APIOptions select what `macro-e2e api` does besides the status report.
type APIOptions struct {
	Build    bool
	Status   bool
	NoStart  bool
	LockWait time.Duration
}

// APIManager builds, stops and starts the e2e Wallets API from the state dir. A process
// is only stopped when its executable is the state-dir binary. Every change happens under
// the wallets-api-restart.lock flock and never while the recording lock is held.
type APIManager struct {
	Paths         Paths
	Recording     RecordingLock
	Health        func(ctx context.Context) (int, bool)
	Executable    func(pid int) (string, bool)
	Signal        func(pid int, signal syscall.Signal) error
	BuildBinary   func(ctx context.Context, output string) error
	Sleep         func(ctx context.Context, duration time.Duration) error
	Now           func() time.Time
	SIGTERMGrace  time.Duration
	HealthTimeout time.Duration
	Out           io.Writer
}

// NewAPIManager wires the real health check, /proc lookups, signals and `go build`.
func NewAPIManager(paths Paths, recording RecordingLock, out, errOut io.Writer) APIManager {
	return APIManager{
		Paths:         paths,
		Recording:     recording,
		Health:        HTTPHealth(APIHealthURL),
		Executable:    ProcessExecutable,
		Signal:        func(pid int, signal syscall.Signal) error { return syscall.Kill(pid, signal) },
		BuildBinary:   GoBuild(paths.BackDir, out, errOut),
		Sleep:         SleepContext,
		Now:           time.Now,
		SIGTERMGrace:  SIGTERMGrace,
		HealthTimeout: HealthTimeout,
		Out:           out,
	}
}

// Run reports (Status) or rebuilds/stops/starts the e2e API.
func (manager APIManager) Run(ctx context.Context, options APIOptions) error {
	if options.Status {
		return manager.status(ctx)
	}
	if err := manager.Recording.RefuseWhileHeld("restarting the e2e API"); err != nil {
		return err
	}
	wait := options.LockWait
	if wait <= 0 {
		wait = DefaultRestartLockWait
	}
	lock, err := AcquireFileLock(ctx, manager.Paths.RestartLock, wait)
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := manager.Recording.RefuseWhileHeld("restarting the e2e API"); err != nil {
		return err
	}

	pid, err := manager.runningPID()
	if err != nil {
		return err
	}
	if options.Build {
		if err := manager.build(ctx); err != nil {
			return err
		}
	}
	if pid != 0 {
		if err := manager.stop(ctx, pid); err != nil {
			return err
		}
	}
	if options.NoStart {
		return nil
	}
	_, err = manager.start(ctx)
	return err
}

func (manager APIManager) status(ctx context.Context) error {
	pid, err := manager.runningPID()
	if err != nil {
		return err
	}
	pidText, healthText := noValue, noValue
	if pid != 0 {
		pidText = strconv.Itoa(pid)
	}
	if code, answered := manager.Health(ctx); answered && code != 0 {
		healthText = strconv.Itoa(code)
	}
	fmt.Fprintf(manager.Out, "pid=%s binary=%s health=%s log=%s\n", pidText, manager.Paths.APIBinary, healthText, manager.Paths.APILog)
	return nil
}

// runningPID is the PID from the pid file when that process still runs the state-dir
// binary (0 when none). A PID running anything else is refused, never touched.
func (manager APIManager) runningPID() (int, error) {
	raw, err := os.ReadFile(manager.Paths.APIPIDFile)
	if err != nil {
		return 0, nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0, nil
	}
	executable, alive := manager.Executable(pid)
	if !alive {
		return 0, nil
	}
	binaryName := filepath.Base(manager.Paths.APIBinary)
	if filepath.Clean(executable) != filepath.Clean(manager.Paths.APIBinary) && !strings.HasPrefix(filepath.Base(executable), binaryName) {
		return 0, fmt.Errorf("PID %d from %s runs %s, not %s; not touching it", pid, manager.Paths.APIPIDFile, executable, manager.Paths.APIBinary)
	}
	return pid, nil
}

func (manager APIManager) build(ctx context.Context) error {
	if err := EnsurePrivateDir(filepath.Dir(manager.Paths.APIBinary)); err != nil {
		return err
	}
	fresh := manager.Paths.APIBinary + freshBinarySuffix
	fmt.Fprintf(manager.Out, "building %s ...\n", fresh)
	buildContext, cancel := context.WithTimeout(ctx, BuildTimeout)
	defer cancel()
	if err := manager.BuildBinary(buildContext, fresh); err != nil {
		return fmt.Errorf("go build: %w", err)
	}
	if _, err := os.Stat(manager.Paths.APIBinary); err == nil {
		previous := manager.Paths.APIBinary + environBackupInfix + manager.Now().UTC().Format(binaryBackupStamp)
		if err := copyPreservingModeAndTime(manager.Paths.APIBinary, previous); err != nil {
			return err
		}
		fmt.Fprintf(manager.Out, "previous binary kept as %s\n", previous)
	}
	if err := os.Rename(fresh, manager.Paths.APIBinary); err != nil {
		return fmt.Errorf("install %s: %w", manager.Paths.APIBinary, err)
	}
	fmt.Fprintf(manager.Out, "installed %s\n", manager.Paths.APIBinary)
	return nil
}

// stop sends SIGTERM, waits SIGTERMGrace for the process to exit, then SIGKILLs that PID.
func (manager APIManager) stop(ctx context.Context, pid int) error {
	fmt.Fprintf(manager.Out, "stopping PID %d (SIGTERM)\n", pid)
	if err := manager.Signal(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			fmt.Fprintf(manager.Out, "PID %d exited\n", pid)
			return nil
		}
		return fmt.Errorf("SIGTERM %d: %w", pid, err)
	}
	deadline := manager.Now().Add(manager.SIGTERMGrace)
	for manager.Now().Before(deadline) {
		if _, alive := manager.Executable(pid); !alive {
			fmt.Fprintf(manager.Out, "PID %d exited\n", pid)
			return nil
		}
		if err := manager.Sleep(ctx, ExitPollInterval); err != nil {
			return err
		}
	}
	fmt.Fprintf(manager.Out, "PID %d ignored SIGTERM for %ds; SIGKILL\n", pid, int(manager.SIGTERMGrace.Seconds()))
	if err := manager.Signal(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("SIGKILL %d: %w", pid, err)
	}
	if err := manager.Sleep(ctx, KillSettle); err != nil {
		return err
	}
	if _, alive := manager.Executable(pid); alive {
		return fmt.Errorf("PID %d still runs after SIGKILL", pid)
	}
	return nil
}

func (manager APIManager) start(ctx context.Context) (int, error) {
	if info, err := os.Stat(manager.Paths.APIBinary); err != nil || !info.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is missing; run with --build", manager.Paths.APIBinary)
	}
	if _, answered := manager.Health(ctx); answered {
		return 0, fmt.Errorf("%s already answers; another API holds the port", APIHealthURL)
	}
	environment, err := ReadAPIEnviron(manager.Paths.APIEnviron)
	if err != nil {
		return 0, err
	}
	if err := EnsurePrivateDir(filepath.Dir(manager.Paths.APILog)); err != nil {
		return 0, err
	}
	logFile, err := os.OpenFile(manager.Paths.APILog, logFileFlags, PrivateFileMode)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", manager.Paths.APILog, err)
	}
	defer logFile.Close()

	command := exec.Command(manager.Paths.APIBinary)
	command.Dir = manager.Paths.BackDir
	command.Env = EnvironList(environment)
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return 0, fmt.Errorf("start %s: %w", manager.Paths.APIBinary, err)
	}
	pid := command.Process.Pid
	exited := make(chan *os.ProcessState, 1)
	go func() {
		_ = command.Wait()
		exited <- command.ProcessState
	}()
	if err := WritePrivateFile(manager.Paths.APIPIDFile, []byte(strconv.Itoa(pid)+"\n")); err != nil {
		return pid, err
	}

	deadline := manager.Now().Add(manager.HealthTimeout)
	for manager.Now().Before(deadline) {
		select {
		case state := <-exited:
			return pid, fmt.Errorf("API exited with %d; see %s", pythonReturnCode(state), manager.Paths.APILog)
		default:
		}
		if code, answered := manager.Health(ctx); answered && code == httpOK {
			fmt.Fprintf(manager.Out, "started PID %d; health %d\n", pid, httpOK)
			return pid, nil
		}
		if err := manager.Sleep(ctx, HealthPollInterval); err != nil {
			return pid, err
		}
	}
	return pid, fmt.Errorf("API PID %d not healthy after %ds; see %s", pid, int(manager.HealthTimeout.Seconds()), manager.Paths.APILog)
}

// pythonReturnCode is subprocess.returncode: the exit status, or -N when killed by signal N.
func pythonReturnCode(state *os.ProcessState) int {
	if state == nil {
		return exitCodeUnknown
	}
	if status, isUnix := state.Sys().(syscall.WaitStatus); isUnix && status.Signaled() {
		return -int(status.Signal())
	}
	return state.ExitCode()
}

// HTTPHealth returns the status of url (any HTTP status counts as an answer).
func HTTPHealth(url string) func(ctx context.Context) (int, bool) {
	client := &http.Client{Timeout: healthRequestTimeout}
	return func(ctx context.Context) (int, bool) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, false
		}
		response, err := client.Do(request)
		if err != nil {
			return 0, false
		}
		_ = response.Body.Close()
		return response.StatusCode, true
	}
}

// ProcessExecutable is readlink(/proc/<pid>/exe) without " (deleted)"; false once the
// process is gone (or a zombie).
func ProcessExecutable(pid int) (string, bool) {
	executable, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return "", false
	}
	return strings.TrimSuffix(executable, deletedExeSuffix), true
}

// GoBuild compiles the back end (`go build -o output .` in backDir).
func GoBuild(backDir string, out, errOut io.Writer) func(ctx context.Context, output string) error {
	return func(ctx context.Context, output string) error {
		command := exec.CommandContext(ctx, goBinary, "build", "-o", output, ".")
		command.Dir = backDir
		command.Stdout = out
		command.Stderr = errOut
		return command.Run()
	}
}

func copyPreservingModeAndTime(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat %s: %w", source, err)
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read %s: %w", source, err)
	}
	if err := os.WriteFile(destination, content, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write %s: %w", destination, err)
	}
	if err := os.Chmod(destination, info.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod %s: %w", destination, err)
	}
	if err := os.Chtimes(destination, info.ModTime(), info.ModTime()); err != nil {
		return fmt.Errorf("preserve mtime of %s: %w", destination, err)
	}
	return nil
}
