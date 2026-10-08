package keyexport

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var outputTestNow = time.Date(2026, 10, 3, 4, 5, 6, 0, time.UTC)

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s has mode %04o, want %04o", path, info.Mode().Perm(), want)
	}
}

func TestPrepare_OutputPath_DefaultIsUnderHomeWith0700DirAnd0600File(t *testing.T) {
	home := t.TempDir()
	path, err := PrepareOutputPath(OutputRequest{Home: home, Now: outputTestNow})
	if err != nil {
		t.Fatal(err)
	}
	resolvedHome, _ := filepath.EvalSymlinks(home)
	want := filepath.Join(resolvedHome, DefaultExportSubdir, "wallet-keys-20261003T040506Z.zip")
	if path != want {
		t.Fatalf("path %s, want %s", path, want)
	}
	requireMode(t, filepath.Dir(path), ArchiveDirMode)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preparing must not create the file")
	}

	output := NewArchiveOutput(path)
	if err := output.Write(func(w io.Writer) error { _, err := w.Write([]byte("zip")); return err }); err != nil {
		t.Fatal(err)
	}
	requireMode(t, path, ArchiveFileMode)
	if _, err := PrepareOutputPath(OutputRequest{Home: home, Now: outputTestNow}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("an existing archive must not be overwritten, got %v", err)
	}
}

func TestPrepare_OutputPath_TightensTheDefaultDirButRefusesALooseCustomDir(t *testing.T) {
	home := t.TempDir()
	defaultDir := filepath.Join(home, DefaultExportSubdir)
	if err := os.MkdirAll(defaultDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(defaultDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareOutputPath(OutputRequest{Home: home, Now: outputTestNow}); err != nil {
		t.Fatal(err)
	}
	requireMode(t, defaultDir, ArchiveDirMode)

	loose := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareOutputPath(OutputRequest{Requested: filepath.Join(loose, "keys.zip"), Now: outputTestNow}); err == nil {
		t.Fatal("a --out directory readable by others must be refused")
	}
	requireMode(t, loose, 0o755)

	created := filepath.Join(t.TempDir(), "new", "nested", "keys.zip")
	if _, err := PrepareOutputPath(OutputRequest{Requested: created, Now: outputTestNow}); err != nil {
		t.Fatal(err)
	}
	requireMode(t, filepath.Dir(created), ArchiveDirMode)
}

func TestPrepare_OutputPath_RefusesPathsInsideTheRepository(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, goModFileName), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(repo, "app", "deep")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	roots := RepositoryRoots(nested, func(string) (string, error) { return "", errors.New("not a git repo") })
	if len(roots) != 1 {
		t.Fatalf("module root not found from a nested dir: %v", roots)
	}

	outside := t.TempDir()
	if err := os.Chmod(outside, ArchiveDirMode); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(outside, "looks-outside")
	if err := os.Symlink(nested, link); err != nil {
		t.Fatal(err)
	}
	for _, requested := range []string{
		filepath.Join(repo, "keys.zip"),
		filepath.Join(nested, "not-yet", "keys.zip"),
		filepath.Join(link, "keys.zip"),
		filepath.Join(outside, "..", filepath.Base(repo), "keys.zip"),
	} {
		_, err := PrepareOutputPath(OutputRequest{Requested: requested, Now: outputTestNow, ForbiddenRoots: roots})
		if err == nil || !strings.Contains(err.Error(), "inside the repository") {
			t.Fatalf("%s must be refused, got %v", requested, err)
		}
	}
	if _, err := os.Stat(filepath.Join(nested, "not-yet")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused path must not create directories inside the repository")
	}
	if _, err := PrepareOutputPath(OutputRequest{Requested: filepath.Join(outside, "keys.zip"), Now: outputTestNow, ForbiddenRoots: roots}); err != nil {
		t.Fatalf("a path outside the repository must be accepted: %v", err)
	}
}

func TestRepository_Roots_FindsThisModuleAndGitToplevel(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	roots := RepositoryRoots(cwd, func(string) (string, error) { return "/git/top", nil })
	moduleRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))
	if len(roots) != 2 || roots[0] != moduleRoot || roots[1] != "/git/top" {
		t.Fatalf("roots %v, want [%s /git/top]", roots, moduleRoot)
	}
	_, err = PrepareOutputPath(OutputRequest{Requested: filepath.Join(moduleRoot, "storage", "keys.zip"), Now: outputTestNow, ForbiddenRoots: roots})
	if err == nil {
		t.Fatal("the backend repository itself must be refused")
	}
}

func TestPrepare_OutputPath_RequiresAZipName(t *testing.T) {
	if _, err := PrepareOutputPath(OutputRequest{Requested: filepath.Join(t.TempDir(), "keys.tar"), Now: outputTestNow}); err == nil {
		t.Fatal("non-.zip output must be refused")
	}
	if _, err := PrepareOutputPath(OutputRequest{Now: outputTestNow}); err == nil {
		t.Fatal("the default path needs a home directory")
	}
}

func TestArchive_Output_IsExclusiveAndRemovesPartialFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.zip")
	failing := NewArchiveOutput(path)
	err := failing.Write(func(w io.Writer) error {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 10_000))
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("a failing writer must fail the write")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a failed write must remove the partial file")
	}

	if err := os.WriteFile(path, []byte("someone else's"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewArchiveOutput(path).Write(func(w io.Writer) error { return nil }); err == nil {
		t.Fatal("an existing file must never be overwritten")
	}
	if content, _ := os.ReadFile(path); string(content) != "someone else's" {
		t.Fatal("the existing file was modified")
	}

	link := filepath.Join(dir, "link.zip")
	if err := os.Symlink(filepath.Join(dir, "target.zip"), link); err != nil {
		t.Fatal(err)
	}
	if err := NewArchiveOutput(link).Write(func(w io.Writer) error { return nil }); err == nil {
		t.Fatal("a symlink must not be followed")
	}
	if _, err := os.Stat(filepath.Join(dir, "target.zip")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the symlink target must not be created")
	}

	done := filepath.Join(dir, "done.zip")
	finished := NewArchiveOutput(done)
	if err := finished.Write(func(w io.Writer) error { _, err := w.Write([]byte("ok")); return err }); err != nil {
		t.Fatal(err)
	}
	finished.RemoveIfPartial()
	if _, err := os.Stat(done); err != nil {
		t.Fatal("a finished archive must survive RemoveIfPartial")
	}
}
