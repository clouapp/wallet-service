package keyexport

import (
	"bytes"
	"testing"
	"time"

	"github.com/yeka/zip"
)

func TestWriteEncryptedZip_EveryEntryIsAES256AndNeedsThePassword(t *testing.T) {
	files := []ArchiveFile{
		{Name: "INSTRUCOES.md", Data: []byte("leia")},
		{Name: "wallets/x/wallet.json", Data: []byte(`{"secret":"material"}`)},
	}
	var archive bytes.Buffer
	if err := WriteEncryptedZip(&archive, []byte(testArchivePassword), files, time.Now()); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(archive.Bytes(), []byte("material")) {
		t.Fatal("plaintext leaked into the archive")
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range reader.File {
		if !file.IsEncrypted() {
			t.Fatalf("%s is not encrypted", file.Name)
		}
		if file.Mode().Perm() != archiveEntryMode {
			t.Fatalf("%s has mode %04o", file.Name, file.Mode().Perm())
		}
	}

	contents, err := ReadEncryptedZip(bytes.NewReader(archive.Bytes()), int64(archive.Len()), []byte(testArchivePassword))
	if err != nil || string(contents["wallets/x/wallet.json"]) != `{"secret":"material"}` {
		t.Fatalf("right password: %v", err)
	}
	for _, wrong := range []string{"", "zip-fixture-password-9f3c", testArchivePassword[:len(testArchivePassword)-1]} {
		if _, err := ReadEncryptedZip(bytes.NewReader(archive.Bytes()), int64(archive.Len()), []byte(wrong)); err == nil {
			t.Fatalf("password %q must not open the archive", wrong)
		}
	}
}

func TestWriteEncryptedZip_RefusesBadInput(t *testing.T) {
	var sink bytes.Buffer
	ok := []ArchiveFile{{Name: "a.json", Data: []byte("{}")}}
	cases := map[string]struct {
		password []byte
		files    []ArchiveFile
	}{
		"no password":     {nil, ok},
		"no files":        {[]byte(testArchivePassword), nil},
		"duplicate names": {[]byte(testArchivePassword), append(append([]ArchiveFile{}, ok...), ok...)},
		"absolute name":   {[]byte(testArchivePassword), []ArchiveFile{{Name: "/etc/x"}}},
		"parent escape":   {[]byte(testArchivePassword), []ArchiveFile{{Name: "../x"}}},
	}
	for name, tc := range cases {
		if err := WriteEncryptedZip(&sink, tc.password, tc.files, time.Now()); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
}
