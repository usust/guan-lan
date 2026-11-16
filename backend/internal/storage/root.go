package storage

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

type Root struct {
	Path       string
	PlatformID string
	lock       *flock.Flock
}

func Open(path string) (*Root, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("root must be absolute")
	}
	path = filepath.Clean(path)
	// Reject symlinks in existing path components, including the root.
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("root path contains symlink: %s", p)
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	if err := regularOrAbsent(filepath.Join(path, ".lock")); err != nil {
		return nil, err
	}
	l := flock.New(filepath.Join(path, ".lock"))
	ok, err := l.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("root is already in use")
	}
	success := false
	defer func() {
		if !success {
			_ = l.Unlock()
		}
	}()
	for _, d := range []string{"config", "data/database", "uploads/packages", "uploads/attachments", "images/archives", "challenges/templates", "instances/runtime", "logs", "temp", "backups"} {
		target := filepath.Join(path, d)
		for p := target; p != path; p = filepath.Dir(p) {
			if info, e := os.Lstat(p); e == nil && info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("managed directory contains symlink: %s", p)
			}
		}
		if err := os.MkdirAll(target, 0700); err != nil {
			return nil, err
		}
	}
	for _, name := range []string{".huashan-root.json", "data/database/huashan.db", "data/database/huashan.db-wal", "data/database/huashan.db-shm", "data/database/huashan.db-journal"} {
		if err := regularOrAbsent(filepath.Join(path, name)); err != nil {
			return nil, err
		}
	}
	marker := filepath.Join(path, ".huashan-root.json")
	var m struct {
		PlatformID string `json:"platform_id"`
	}
	b, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return nil, err
		}
		m.PlatformID = hex.EncodeToString(id)
		b, _ = json.Marshal(m)
		if err = os.WriteFile(marker, b, 0600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else if err = json.Unmarshal(b, &m); err != nil || m.PlatformID == "" {
		return nil, fmt.Errorf("invalid root marker")
	}
	probe, err := os.CreateTemp(filepath.Join(path, "temp"), "probe-")
	if err != nil {
		return nil, err
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	success = true
	return &Root{Path: path, PlatformID: m.PlatformID, lock: l}, nil
}

// regularOrAbsent rejects symlinks (including dangling ones) and special files.
// Lstat is intentional: Stat would follow a link to an otherwise regular file.
func regularOrAbsent(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect storage file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("storage file must be a regular file: %s", path)
	}
	return nil
}

func (r *Root) Close() error         { return r.lock.Unlock() }
func (r *Root) DatabasePath() string { return filepath.Join(r.Path, "data", "database", "huashan.db") }
