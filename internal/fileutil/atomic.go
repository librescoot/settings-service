package fileutil

import (
	"fmt"
	"os"
)

// AtomicWrite creates a file atomically by calling writeFn with a temporary file,
// syncing to disk, then renaming over the target path. This prevents corruption
// from crashes or power loss during the write.
func AtomicWrite(path string, perm os.FileMode, writeFn func(f *os.File) error) error {
	tmpPath := path + ".tmp"

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	if err := writeFn(f); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	f.Close()

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}
