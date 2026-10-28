package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWrite syncs the contents and renamed directory entry before returning.
func AtomicWrite(path string, perm os.FileMode, writeFn func(f *os.File) error) error {
	tmpPath := path + ".tmp"

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	// OpenFile does not apply perm when a temporary file already exists.
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to protect temp file: %w", err)
	}

	if err := writeFn(f); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("failed to open parent directory: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("failed to sync parent directory: %w", err)
	}
	return nil
}
