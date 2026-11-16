package database

import (
	"path/filepath"
	"testing"
)

func TestIdentityAndSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path, "platform-a")
	if err != nil {
		t.Fatal(err)
	}
	var enabled int
	if err = db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("foreign keys: %d %v", enabled, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if wrong, err := Open(path, "platform-b"); err == nil {
		wrong.Close()
		t.Fatal("accepted mismatched platform")
	}
	db, err = Open(path, "platform-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if newer, err := Open(path, "platform-a"); err == nil {
		newer.Close()
		t.Fatal("accepted future schema")
	}
}
