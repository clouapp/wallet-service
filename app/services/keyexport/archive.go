package keyexport

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/yeka/zip"
)

const archiveEntryMode os.FileMode = 0o600

// WriteEncryptedZip writes files as a WinZip AES-256 (AE-2) zip: every entry is
// deflated, then encrypted and authenticated with a key derived from password.
// Entry names are not encrypted, so they must not carry secrets.
func WriteEncryptedZip(w io.Writer, password []byte, files []ArchiveFile, modTime time.Time) error {
	if w == nil {
		return errors.New("archive writer is required")
	}
	if len(password) == 0 {
		return errors.New("archive password is required")
	}
	if len(files) == 0 {
		return errors.New("archive has no files")
	}
	if err := requireUniqueEntryNames(files); err != nil {
		return err
	}
	archive := zip.NewWriter(w)
	passwordString := string(password)
	for _, file := range files {
		header := &zip.FileHeader{Name: file.Name, Method: zip.Deflate}
		header.SetModTime(modTime)
		header.SetMode(archiveEntryMode)
		header.SetPassword(passwordString)
		header.SetEncryptionMethod(zip.AES256Encryption)
		entry, err := archive.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("add %s to the archive: %w", file.Name, err)
		}
		if _, err := entry.Write(file.Data); err != nil {
			return fmt.Errorf("write %s to the archive: %w", file.Name, err)
		}
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("finish the archive: %w", err)
	}
	return nil
}

func requireUniqueEntryNames(files []ArchiveFile) error {
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		if file.Name == "" || strings.HasPrefix(file.Name, "/") || strings.Contains(file.Name, "..") {
			return fmt.Errorf("archive entry name %q is not a safe relative path", file.Name)
		}
		if seen[file.Name] {
			return fmt.Errorf("archive entry %s appears twice", file.Name)
		}
		seen[file.Name] = true
	}
	return nil
}

// ReadEncryptedZip opens every entry of an AES zip with password and returns their
// contents by name; a wrong password fails. The caller zeroes the returned data.
func ReadEncryptedZip(r io.ReaderAt, size int64, password []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	contents := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		if !file.IsEncrypted() {
			wipeContents(contents)
			return nil, fmt.Errorf("archive entry %s is not encrypted", file.Name)
		}
		file.SetPassword(string(password))
		data, err := readEntry(file)
		if err != nil {
			wipeContents(contents)
			return nil, fmt.Errorf("read %s: %w", file.Name, err)
		}
		contents[file.Name] = data
	}
	return contents, nil
}

func readEntry(file *zip.File) ([]byte, error) {
	entry, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer entry.Close()
	return io.ReadAll(entry)
}

func wipeContents(contents map[string][]byte) {
	for _, data := range contents {
		zeroBytes(data)
	}
}
