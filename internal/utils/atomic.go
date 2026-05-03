package utils

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteFile writes data to path atomically — writes to a temp
// file in the same directory, fsyncs it, then renames into place. The
// temp-file name carries a random suffix so concurrent writers targeting
// the same path don't stomp each other's tmp files (baseline T71). A
// deterministic `.tmp` suffix is fine under a single-writer mutex but
// breaks the moment a panic-recovery wrapper retries or a parallel
// writer shows up.
//
// perm applies to the final file. 0600 is the right default for files
// that contain API keys, webhook URLs, or other bearer credentials;
// 0644 is fine for purely cosmetic state.
//
// On error the temp file is removed on a best-effort basis. Callers
// should check the error — a failed atomic write usually means the
// filesystem is full or permissions are wrong, not a transient issue.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	// 8 random bytes = 16 hex chars — enough entropy that two concurrent
	// writers will never collide in practice, and short enough that the
	// tmp filename stays readable in directory listings.
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Errorf("atomic write: random suffix: %w", err)
	}
	tmp := filepath.Join(dir, "."+filepath.Base(path)+".tmp-"+hex.EncodeToString(buf[:]))

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("atomic write: create tmp: %w", err)
	}

	// Write-and-close-and-rename pattern. If the write or fsync fails we
	// must remove the tmp file ourselves — Rename won't happen, and the
	// tmp file would otherwise accumulate on repeat failures.
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("atomic write: write tmp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("atomic write: fsync tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("atomic write: close tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("atomic write: rename tmp->final: %w", err)
	}
	return nil
}
