// Package architecture holds the machine-checked subset of .ai/guidelines:
// import direction between layers and the rules a reviewer would otherwise
// have to remember. Every check runs in REPORT mode until the migration phase
// that owns it closes (see report.go).
package architecture

import (
	"bufio"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// SourceFile is one parsed Go file of the module.
type SourceFile struct {
	// Path is the slash-separated path relative to the module root.
	Path string
	// Dir is the slash-separated directory relative to the module root ("." for the root).
	Dir string
	// IsTest is true for *_test.go files.
	IsTest bool
	// IsGenerated is true for files carrying the standard "Code generated" header.
	IsGenerated bool
	AST         *ast.File
}

// Imports returns the import paths of the file.
func (f *SourceFile) Imports() []string {
	paths := make([]string, 0, len(f.AST.Imports))
	for _, spec := range f.AST.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

// Module is the parsed source tree of the repository.
type Module struct {
	Root  string
	Path  string
	Files []*SourceFile
}

// skippedDirectories never hold module source: build output, vendored or
// generated trees, fixtures.
var skippedDirectories = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"tmp":          true,
	"testdata":     true,
}

// LoadModule parses every Go file of the repository that holds this package.
func LoadModule() (*Module, error) {
	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	modulePath, err := readModulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	module := &Module{Root: root, Path: modulePath}
	fileSet := token.NewFileSet()
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (skippedDirectories[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		parsed, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relative path of %s: %w", path, err)
		}
		relative = filepath.ToSlash(relative)
		module.Files = append(module.Files, &SourceFile{
			Path:        relative,
			Dir:         slashDir(relative),
			IsTest:      strings.HasSuffix(relative, "_test.go"),
			IsGenerated: ast.IsGenerated(parsed),
			AST:         parsed,
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(module.Files, func(i, j int) bool { return module.Files[i].Path < module.Files[j].Path })
	return module, nil
}

// ImportPathOf returns the import path of a module directory.
func (m *Module) ImportPathOf(dir string) string {
	if dir == "." {
		return m.Path
	}
	return m.Path + "/" + dir
}

// RelativeOf returns the module-relative directory of an import path, and
// false when the path is outside the module.
func (m *Module) RelativeOf(importPath string) (string, bool) {
	if importPath == m.Path {
		return ".", true
	}
	if strings.HasPrefix(importPath, m.Path+"/") {
		return strings.TrimPrefix(importPath, m.Path+"/"), true
	}
	return "", false
}

// ProductionFiles returns the non-test, non-generated files under any of the
// given directory prefixes ("" matches everything).
func (m *Module) ProductionFiles(prefixes ...string) []*SourceFile {
	var files []*SourceFile
	for _, file := range m.Files {
		if file.IsTest || file.IsGenerated {
			continue
		}
		if len(prefixes) == 0 || hasAnyPrefix(file.Dir, prefixes) {
			files = append(files, file)
		}
	}
	return files
}

// hasAnyPrefix reports whether dir is one of the prefixes or nested under one.
func hasAnyPrefix(dir string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if prefix == "" || dir == prefix || strings.HasPrefix(dir, prefix+"/") {
			return true
		}
	}
	return false
}

func slashDir(relative string) string {
	dir := filepath.ToSlash(filepath.Dir(relative))
	if dir == "" {
		return "."
	}
	return dir
}

func moduleRoot() (string, error) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("resolve module root: caller information is unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..")), nil
}

func readModulePath(goModPath string) (string, error) {
	file, err := os.Open(filepath.Clean(goModPath))
	if err != nil {
		return "", fmt.Errorf("open go.mod: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	return "", errors.New("go.mod has no module line")
}
