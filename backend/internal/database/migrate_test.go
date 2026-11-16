package database

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func legacyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ddl := strings.ReplaceAll(strings.ReplaceAll(initialSQL, "CREATE TABLE ", "CREATE TABLE IF NOT EXISTS "), "CREATE INDEX ", "CREATE INDEX IF NOT EXISTS ")
	if _, err = db.Exec(ddl + `; INSERT INTO metadata VALUES('platform_id','p'); PRAGMA user_version=1;
 INSERT INTO users VALUES('u','alice','Alice','hash','PLAYER','ACTIVE',123);
 INSERT INTO sessions VALUES('token','u','csrf',456);
 INSERT INTO audit_logs VALUES('a','u','created','u',123);`); err != nil {
		t.Fatal(err)
	}
	return db
}

func scalar(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLegacyMigrationPreservesData(t *testing.T) {
	db := legacyDB(t)
	for i := 0; i < 2; i++ {
		if err := migrate(db, "p", []migration{{initialSQL}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"users", "sessions", "audit_logs", "schema_migrations"} {
		if scalar(t, db, "SELECT count(*) FROM "+table) != 1 {
			t.Fatalf("lost or duplicated %s", table)
		}
	}
	var hash string
	if err := db.QueryRow("SELECT password_hash FROM users WHERE id='u'").Scan(&hash); err != nil || hash != "hash" {
		t.Fatal("credential changed", err)
	}
}

func TestRejectedMigrationDoesNotWrite(t *testing.T) {
	for _, kind := range []string{"identity", "incomplete", "future"} {
		t.Run(kind, func(t *testing.T) {
			db := legacyDB(t)
			id := "p"
			switch kind {
			case "identity":
				id = "other"
			case "incomplete":
				if _, err := db.Exec("DROP INDEX sessions_expiry"); err != nil {
					t.Fatal(err)
				}
			case "future":
				if _, err := db.Exec("PRAGMA user_version=99"); err != nil {
					t.Fatal(err)
				}
			}
			if err := migrate(db, id, []migration{{initialSQL}}); err == nil {
				t.Fatal("accepted invalid database")
			}
			if scalar(t, db, "SELECT count(*) FROM sqlite_master WHERE name='schema_migrations'") != 0 {
				t.Fatal("wrote migration ledger")
			}
		})
	}
}

func TestMigrationRollbackAndChecksum(t *testing.T) {
	db := legacyDB(t)
	if err := migrate(db, "p", []migration{{initialSQL}, {"CREATE TABLE partial(id TEXT); INVALID SQL;"}}); err == nil {
		t.Fatal("expected failure")
	}
	if scalar(t, db, "SELECT count(*) FROM sqlite_master WHERE name IN ('partial','schema_migrations')") != 0 {
		t.Fatal("partial transaction persisted")
	}
	if scalar(t, db, "PRAGMA user_version") != 1 {
		t.Fatal("version advanced")
	}
	if err := migrate(db, "p", []migration{{initialSQL}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE schema_migrations SET checksum='tampered'"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db, "p", []migration{{initialSQL}}); err == nil {
		t.Fatal("checksum ignored")
	}
}

func TestProductionUpgradeFromLegacyV1(t *testing.T) {
	db := legacyDB(t)
	var seq int
	var name, path string
	if err := db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if scalar(t, upgraded, "PRAGMA user_version") != 17 {
		t.Fatal("wrong version")
	}
	for _, table := range []string{"users", "sessions", "audit_logs"} {
		if scalar(t, upgraded, "SELECT count(*) FROM "+table) != 1 {
			t.Fatal("legacy data lost", table)
		}
	}
	var key string
	if err = upgraded.QueryRow("SELECT value FROM metadata WHERE key='flag_hmac_key'").Scan(&key); err != nil || len(key) != 64 {
		t.Fatal("missing flag key", err)
	}
	if err = upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var after string
	reopened.QueryRow("SELECT value FROM metadata WHERE key='flag_hmac_key'").Scan(&after)
	if key != after {
		t.Fatal("flag key rotated on restart")
	}
}

func TestHierarchyUpgradePreservesFlatTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v13.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	migrations := []migration{{initialSQL}, {competitionsSQL}, {runtimeSQL}, {questionLibrarySQL}, {attachmentsSQL}, {stagedPackagesSQL}, {packageUploadsSQL}, {subtaskResourcesSQL}, {questionSourcesSQL}, {solutionDocumentsSQL}, {attachmentUploadsSQL}, {runtimeBindingsSQL}, {questionTaxonomySQL}}
	if err = migrate(db, "p", migrations); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO question_types(name) VALUES('自定义分类')"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO challenge_sets VALUES('s','原题','', 'DRAFT',0,100,7,0,0)"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO challenges(id,set_id,title,description,category,difficulty,position,environment_json,tags_json) VALUES('q','s','原题','','自定义分类','EASY',0,'null','[\"考点\"]')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	upgraded, err := Open(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if scalar(t, upgraded, "SELECT count(*) FROM question_types WHERE name='自定义分类' AND parent_name IS NULL") != 1 {
		t.Fatal("flat type lost")
	}
	if scalar(t, upgraded, "SELECT count(*) FROM challenges WHERE category='自定义分类' AND tags_json='[\"考点\"]'") != 1 {
		t.Fatal("question metadata changed")
	}
	if scalar(t, upgraded, "SELECT version FROM challenge_sets WHERE id='s'") != 7 {
		t.Fatal("version changed during hierarchy upgrade")
	}
}

func TestKnowledgeMigrationPreservesLegacyTags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v14.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	migrations := []migration{{initialSQL}, {competitionsSQL}, {runtimeSQL}, {questionLibrarySQL}, {attachmentsSQL}, {stagedPackagesSQL}, {packageUploadsSQL}, {subtaskResourcesSQL}, {questionSourcesSQL}, {solutionDocumentsSQL}, {attachmentUploadsSQL}, {runtimeBindingsSQL}, {questionTaxonomySQL}, {questionTypeHierarchySQL}}
	if err = migrate(db, "p", migrations); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO challenge_sets VALUES('s','旧题库','','DRAFT',0,100,7,0,0)"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO challenges(id,set_id,title,description,category,difficulty,position,environment_json,tags_json) VALUES('q','s','旧题','','WEB','EASY',0,'null','["JWT","SQL注入"]')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	upgraded, err := Open(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if scalar(t, upgraded, "SELECT count(*) FROM knowledge_points WHERE name IN ('JWT','SQL注入') AND parent_name IS NULL") != 2 {
		t.Fatal("tags not migrated")
	}
	if scalar(t, upgraded, "SELECT count(*) FROM knowledge_point_types") != 0 {
		t.Fatal("invented type associations")
	}
	if scalar(t, upgraded, "SELECT version FROM challenge_sets WHERE id='s'") != 7 {
		t.Fatal("question version changed")
	}
	var raw string
	if err = upgraded.QueryRow("SELECT tags_json FROM challenges WHERE id='q'").Scan(&raw); err != nil || raw != `["JWT","SQL注入"]` {
		t.Fatal(raw, err)
	}
}

func TestKnowledgeOwnershipMigrationKeepsAmbiguousTreesPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v15.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	migrations := []migration{{initialSQL}, {competitionsSQL}, {runtimeSQL}, {questionLibrarySQL}, {attachmentsSQL}, {stagedPackagesSQL}, {packageUploadsSQL}, {subtaskResourcesSQL}, {questionSourcesSQL}, {solutionDocumentsSQL}, {attachmentUploadsSQL}, {runtimeBindingsSQL}, {questionTaxonomySQL}, {questionTypeHierarchySQL}, {knowledgePointsSQL}}
	if err = migrate(db, "p", migrations); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO knowledge_points(name,parent_name) VALUES('认证',NULL),('JWT','认证'),('跨方向',NULL),('交叉子级','跨方向'),('未设置',NULL);
 INSERT INTO knowledge_point_types VALUES('认证','WEB'),('跨方向','WEB'),('交叉子级','逆向');`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	upgraded, err := Open(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if scalar(t, upgraded, "SELECT count(*) FROM knowledge_points WHERE name IN ('认证','JWT') AND owner_type='WEB'") != 2 {
		t.Fatal("unambiguous subtree not assigned")
	}
	if scalar(t, upgraded, "SELECT count(*) FROM knowledge_points WHERE name IN ('跨方向','交叉子级','未设置') AND owner_type IS NULL") != 3 {
		t.Fatal("ambiguous ownership was guessed")
	}
	if scalar(t, upgraded, "SELECT count(*) FROM knowledge_point_types") != 3 {
		t.Fatal("legacy associations lost")
	}
}
