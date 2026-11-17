package logger

import (
	"fmt"
	"os"
)

// EnsureDir creates dir with mode when missing and corrects the mode of an
// existing directory created by an older build. A failed correction is
// returned so the caller can log what it was trying to do; the directory is
// still usable if the process already has access.
func EnsureDir(dir string, mode os.FileMode) error {
	if err := os.MkdirAll(dir, mode); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	if fi.Mode().Perm() == mode {
		return nil
	}
	if err := os.Chmod(dir, mode|os.ModeSetgid&fi.Mode()); err != nil {
		return fmt.Errorf("correct mode of %s from %o to %o: %w", dir, fi.Mode().Perm(), mode, err)
	}
	return nil
}
