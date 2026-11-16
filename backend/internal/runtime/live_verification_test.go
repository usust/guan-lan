package runtime

import (
	"context"
	"database/sql"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in test against a disposable SQLite snapshot and package directory.
// Never point this at a production root: verification updates its database.
func TestLiveMultiContainerVerification(t *testing.T) {
	root := os.Getenv("HUASHAN_LIVE_VERIFY_ROOT")
	if root == "" {
		t.Skip("set HUASHAN_LIVE_VERIFY_ROOT to a disposable runtime snapshot")
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "verify.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var question string
	var version int
	err = db.QueryRow("SELECT q.id,s.version FROM challenges q JOIN challenge_sets s ON s.id=q.set_id WHERE q.environment_json!='null' AND s.status IN ('DRAFT','CLOSED') ORDER BY json_array_length(q.environment_json,'$.services') DESC LIMIT 1").Scan(&question, &version)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(db, root, "huashan-integration-verification", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithProgress(context.Background(), func(message string) { t.Log(time.Now().Format("15:04:05"), message) })
	if err = m.Verify(ctx, question, version); err != nil {
		t.Fatal(err)
	}
	// Simulate the next startup with an unavailable TAR path. The persisted
	// image records must resolve all images using Docker inspect alone.
	manifest, _, _, sha, err := m.packageInfo(ctx, question)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	ids, err := m.Engine.ReadyImages(ctx, filepath.Join(root, "intentionally-unavailable.tar"), sha, filepath.Join(root, "temp"), manifest)
	if err != nil || len(ids) != len(manifest.Images) {
		t.Fatalf("warm startup did not reuse images: %v", err)
	}
	t.Logf("Warm image preparation: %s for %d images; TAR unavailable", time.Since(started), len(ids))
	var count int
	if err = db.QueryRow("SELECT count(*) FROM runtime_verifications WHERE challenge_id=?", question).Scan(&count); err != nil || count != 1 {
		t.Fatalf("verification not saved: %d %v", count, err)
	}
}
