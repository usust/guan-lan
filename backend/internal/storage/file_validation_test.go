package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegularOrAbsent(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "regular")
	if err := os.WriteFile(file, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(base, "absent")} {
		if err := regularOrAbsent(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := regularOrAbsent(base); err == nil {
		t.Fatal("directory accepted")
	}
	for _, target := range []string{file, filepath.Join(base, "missing-target")} {
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if err := regularOrAbsent(link); err == nil {
			t.Fatal("symlink accepted")
		}
	}
}

func TestRootRejectsUnsafeSensitiveFiles(t *testing.T) {
	for _, name := range []string{".lock", ".huashan-root.json", "data/database/huashan.db", "data/database/huashan.db-wal", "data/database/huashan.db-shm", "data/database/huashan.db-journal"} {
		for _, kind := range []string{"symlink", "directory"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				base, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				root := filepath.Join(base, "runtime")
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(base, "outside")
				if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink" {
					err = os.Symlink(target, path)
				} else {
					err = os.Mkdir(path, 0700)
				}
				if err != nil {
					t.Fatal(err)
				}
				if r, err := Open(root); err == nil {
					r.Close()
					t.Fatal("unsafe file accepted")
				}
				data, err := os.ReadFile(target)
				if err != nil || string(data) != "unchanged" {
					t.Fatalf("outside file modified: %v", err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				r, err := Open(root)
				if err != nil {
					t.Fatalf("root unusable after failed open: %v", err)
				}
				if err := r.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
