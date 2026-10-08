package keyexport

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	ArchiveFileMode       os.FileMode = 0o600
	ArchiveDirMode        os.FileMode = 0o700
	DefaultExportSubdir               = ".local/state/macro-wallets/exports"
	archiveNamePrefix                 = "wallet-keys-"
	archiveExtension                  = ".zip"
	archiveStampLayout                = "20060102T150405Z"
	groupOtherPermissions os.FileMode = 0o077
	goModFileName                     = "go.mod"
)

// DefaultArchivePath is <home>/.local/state/macro-wallets/exports/wallet-keys-<UTC stamp>.zip.
func DefaultArchivePath(home string, now time.Time) string {
	return filepath.Join(home, DefaultExportSubdir, archiveNamePrefix+now.UTC().Format(archiveStampLayout)+archiveExtension)
}

// OutputRequest is where the archive may go. Requested is the --out value ("" for
// the default); ForbiddenRoots are directories (the repository) it must stay out of.
type OutputRequest struct {
	Requested      string
	Home           string
	Now            time.Time
	ForbiddenRoots []string
}

// PrepareOutputPath resolves the archive path (absolute, symlinks of existing
// parents resolved), refuses any path inside a forbidden root or that already
// exists, and makes sure its directory exists, is owned by us and is 0700. The
// default directory is created or tightened to 0700; a --out directory that is
// readable by others is refused rather than changed.
func PrepareOutputPath(request OutputRequest) (string, error) {
	requested := strings.TrimSpace(request.Requested)
	usingDefault := requested == ""
	if usingDefault {
		if request.Home == "" {
			return "", errors.New("home directory is unknown; pass --out")
		}
		requested = DefaultArchivePath(request.Home, request.Now)
	}
	if !strings.EqualFold(filepath.Ext(requested), archiveExtension) {
		return "", fmt.Errorf("output file must end in %s", archiveExtension)
	}
	absolute, err := filepath.Abs(requested)
	if err != nil {
		return "", fmt.Errorf("resolve output path: %w", err)
	}
	resolved, err := resolveExistingPrefix(absolute)
	if err != nil {
		return "", err
	}
	if err := refuseInsideRoots(resolved, request.ForbiddenRoots); err != nil {
		return "", err
	}
	dir := filepath.Dir(resolved)
	if err := prepareArchiveDir(dir, usingDefault); err != nil {
		return "", err
	}
	final, err := resolveExistingPrefix(resolved)
	if err != nil {
		return "", err
	}
	if err := refuseInsideRoots(final, request.ForbiddenRoots); err != nil {
		return "", err
	}
	if _, err := os.Lstat(final); err == nil {
		return "", fmt.Errorf("%s already exists; refusing to overwrite", final)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("check %s: %w", final, err)
	}
	return final, nil
}

// resolveExistingPrefix resolves symlinks in the longest existing prefix of an
// absolute path and appends the part that does not exist yet.
func resolveExistingPrefix(absolute string) (string, error) {
	existing, rest := filepath.Clean(absolute), ""
	for {
		resolved, err := filepath.EvalSymlinks(existing)
		if err == nil {
			return filepath.Join(resolved, rest), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("resolve %s: %w", existing, err)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("resolve %s: no existing ancestor", absolute)
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
}

func refuseInsideRoots(path string, roots []string) error {
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		resolvedRoot, err := resolveExistingPrefix(root)
		if err != nil {
			return err
		}
		if isWithin(path, resolvedRoot) {
			return fmt.Errorf("refusing to write keys inside the repository (%s); choose a path outside it", resolvedRoot)
		}
	}
	return nil
}

func isWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func prepareArchiveDir(dir string, ownedDefault bool) error {
	if err := os.MkdirAll(dir, ArchiveDirMode); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%s is not owned by the current user", dir)
	}
	if info.Mode().Perm()&groupOtherPermissions == 0 {
		return nil
	}
	if !ownedDefault {
		return fmt.Errorf("%s is accessible by group/others (mode %04o); use a 0700 directory", dir, info.Mode().Perm())
	}
	if err := os.Chmod(dir, ArchiveDirMode); err != nil {
		return fmt.Errorf("chmod 700 %s: %w", dir, err)
	}
	return nil
}

// ArchiveOutput writes the archive to a path created exclusively with mode 0600 and
// removes it if writing fails or the process is interrupted (RemoveIfPartial).
type ArchiveOutput struct {
	path     string
	mu       sync.Mutex
	created  bool
	finished bool
}

func NewArchiveOutput(path string) *ArchiveOutput {
	return &ArchiveOutput{path: path}
}

func (o *ArchiveOutput) Path() string { return o.path }

// Write creates the file (never overwriting, never following a symlink), streams
// the archive into it, syncs it, and removes it on any failure.
func (o *ArchiveOutput) Write(write func(io.Writer) error) (err error) {
	if write == nil {
		return errors.New("archive writer is required")
	}
	file, err := os.OpenFile(o.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, ArchiveFileMode)
	if err != nil {
		return fmt.Errorf("create %s: %w", o.path, err)
	}
	o.mu.Lock()
	o.created = true
	o.mu.Unlock()
	defer func() {
		if err != nil {
			_ = file.Close()
			o.RemoveIfPartial()
		}
	}()
	if err := file.Chmod(ArchiveFileMode); err != nil {
		return fmt.Errorf("chmod 600 %s: %w", o.path, err)
	}
	buffered := bufio.NewWriter(file)
	if err := write(buffered); err != nil {
		return err
	}
	if err := buffered.Flush(); err != nil {
		return fmt.Errorf("write %s: %w", o.path, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", o.path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", o.path, err)
	}
	o.mu.Lock()
	o.finished = true
	o.mu.Unlock()
	return nil
}

// RemoveIfPartial deletes the file when this output created it and did not finish.
func (o *ArchiveOutput) RemoveIfPartial() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.created && !o.finished {
		_ = os.Remove(o.path)
		o.created = false
	}
}

// RepositoryRoots are the directories an export must never be written into: the Go
// module root above dir (the directory holding go.mod) and the git toplevel of dir.
func RepositoryRoots(dir string, gitToplevel func(dir string) (string, error)) []string {
	var roots []string
	if moduleRoot, ok := findModuleRoot(dir); ok {
		roots = append(roots, moduleRoot)
	}
	if gitToplevel != nil {
		if top, err := gitToplevel(dir); err == nil && strings.TrimSpace(top) != "" {
			roots = append(roots, strings.TrimSpace(top))
		}
	}
	return roots
}

func findModuleRoot(dir string) (string, bool) {
	current, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for {
		if info, err := os.Stat(filepath.Join(current, goModFileName)); err == nil && !info.IsDir() {
			return current, true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		current = parent
	}
}
