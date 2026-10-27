package fileutil

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestAtomicWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	for _, contents := range []string{"old settings", "new settings", ""} {
		if err := AtomicWrite(path, 0600, func(f *os.File) error {
			_, err := f.WriteString(contents)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != contents {
			t.Fatalf("contents = %q, %v; want %q", got, err, contents)
		}
		if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("temporary file remains: %v", err)
		}
	}
	writeErr := errors.New("write failed")
	if err := AtomicWrite(path, 0600, func(f *os.File) error {
		_, _ = f.WriteString("incomplete")
		return writeErr
	}); !errors.Is(err, writeErr) {
		t.Fatalf("write error = %v, want %v", err, writeErr)
	}
	if got, err := os.ReadFile(path); err != nil || len(got) != 0 {
		t.Fatalf("failed write replaced previous contents: %q, %v", got, err)
	}
}

func TestAtomicWriteDirectorySyncFailure(t *testing.T) {
	if os.Getenv("SETTINGS_TEST_DIRSYNC") == "1" {
		// Keep both fsync calls on one thread for strace's per-thread counter.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		err := AtomicWrite(filepath.Join(t.TempDir(), "settings.toml"), 0600, func(f *os.File) error {
			_, err := f.WriteString("settings")
			return err
		})
		if !errors.Is(err, syscall.EIO) {
			t.Fatalf("directory fsync failure = %v, want EIO", err)
		}
		return
	}
	strace, err := exec.LookPath("strace")
	if err != nil {
		t.Skip("strace required for directory-fsync fault injection")
	}
	cmd := exec.Command(strace, "-f", "-qq", "-o", filepath.Join(t.TempDir(), "trace"),
		"-e", "inject=fsync:error=EIO:when=2", os.Args[0],
		"-test.run=^TestAtomicWriteDirectorySyncFailure$")
	cmd.Env = append(os.Environ(), "SETTINGS_TEST_DIRSYNC=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("directory-fsync regression: %v\n%s", err, output)
	}
}
