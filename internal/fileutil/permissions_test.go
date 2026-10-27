package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteProtectsExistingTemporaryFileBeforeWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path+".tmp", []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path+".tmp", 0644); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWrite(path, 0600, func(file *os.File) error {
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if info.Mode().Perm() != 0600 {
			return os.ErrPermission
		}
		_, err = file.WriteString("private-fixture")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("final file permissions changed")
	}
}
