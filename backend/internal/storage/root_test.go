package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRootLifecycle(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "runtime")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id := r.PlatformID
	if _, err = Open(path); err == nil {
		t.Fatal("duplicate root lock allowed")
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.PlatformID != id {
		t.Fatal("platform identity changed")
	}
	if _, err = os.Stat(filepath.Join(path, "uploads", "packages")); err != nil {
		t.Fatal(err)
	}
}
func TestRejectUnsafePaths(t *testing.T) {
	if _, err := Open("relative"); err == nil {
		t.Fatal("relative root accepted")
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	if _, err := Open(link); err == nil {
		t.Fatal("symlink accepted")
	}
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "uploads")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err == nil {
		t.Fatal("managed directory symlink accepted")
	}
}
