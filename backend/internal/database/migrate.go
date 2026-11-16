package database

import (
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
	"time"
)

//go:embed migrations/001_initial.sql
var initialSQL string

//go:embed migrations/002_competitions.sql
var competitionsSQL string

//go:embed migrations/003_runtime.sql
var runtimeSQL string

//go:embed migrations/004_question_library.sql
var questionLibrarySQL string

//go:embed migrations/005_attachments.sql
var attachmentsSQL string

//go:embed migrations/006_staged_packages.sql
var stagedPackagesSQL string

//go:embed migrations/007_package_uploads.sql
var packageUploadsSQL string

//go:embed migrations/008_subtask_resources.sql
var subtaskResourcesSQL string

//go:embed migrations/009_question_sources.sql
var questionSourcesSQL string

//go:embed migrations/010_solution_documents.sql
var solutionDocumentsSQL string

//go:embed migrations/011_attachment_uploads.sql
var attachmentUploadsSQL string

//go:embed migrations/012_runtime_bindings.sql
var runtimeBindingsSQL string

//go:embed migrations/013_question_taxonomy.sql
var questionTaxonomySQL string

//go:embed migrations/014_question_type_hierarchy.sql
var questionTypeHierarchySQL string

//go:embed migrations/015_knowledge_points.sql
var knowledgePointsSQL string

//go:embed migrations/016_knowledge_ownership.sql
var knowledgeOwnershipSQL string

//go:embed migrations/017_difficulties.sql
var difficultiesSQL string

type migration struct{ sql string }

// All schema changes and ledger updates share a transaction. The caller holds
// the runtime-root process lock; SQLite additionally serializes writers.
func migrate(db *sql.DB, platformID string, migrations []migration) error {
	if platformID == "" {
		return fmt.Errorf("platform identity is required")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version, objects int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version < 0 || version > len(migrations) {
		return fmt.Errorf("unsupported database version %d", version)
	}
	if version == 0 {
		if err = tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&objects); err != nil {
			return err
		}
		if objects != 0 {
			return fmt.Errorf("unversioned nonempty database; refusing initialization")
		}
	} else {
		var identity string
		if err = tx.QueryRow("SELECT value FROM metadata WHERE key='platform_id'").Scan(&identity); err != nil {
			return fmt.Errorf("read platform identity: %w", err)
		}
		if identity != platformID {
			return fmt.Errorf("database belongs to a different platform root")
		}
		if err = validateLegacySchema(tx); err != nil {
			return err
		}
	}
	var ledger int
	if err = tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'").Scan(&ledger); err != nil {
		return err
	}
	if ledger == 0 {
		if version > 1 {
			return fmt.Errorf("migration history missing")
		}
		if _, err = tx.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL)"); err != nil {
			return err
		}
		if version == 1 {
			if _, err = tx.Exec("INSERT INTO schema_migrations VALUES(1,?,?)", checksum(migrations[0].sql), time.Now().UnixMilli()); err != nil {
				return err
			}
		}
	} else {
		var count int
		if err = tx.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
			return err
		}
		if count != version {
			return fmt.Errorf("migration history does not match database version")
		}
		for i := 0; i < version; i++ {
			var sum string
			if err = tx.QueryRow("SELECT checksum FROM schema_migrations WHERE version=?", i+1).Scan(&sum); err != nil {
				return err
			}
			if sum != checksum(migrations[i].sql) {
				return fmt.Errorf("migration %d checksum mismatch", i+1)
			}
		}
	}
	for i := version; i < len(migrations); i++ {
		if _, err = tx.Exec(migrations[i].sql); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if i == 0 {
			if _, err = tx.Exec("INSERT INTO metadata VALUES('platform_id',?)", platformID); err != nil {
				return err
			}
		}
		if _, err = tx.Exec("INSERT INTO schema_migrations VALUES(?,?,?)", i+1, checksum(migrations[i].sql), time.Now().UnixMilli()); err != nil {
			return err
		}
		if _, err = tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", i+1)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func checksum(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

// Legacy v1 was created by the same DDL with IF NOT EXISTS. Check the full
// table/index definitions, not merely the presence of similarly named columns.
func validateLegacySchema(tx *sql.Tx) error {
	normalize := func(s string) string {
		s = strings.ReplaceAll(strings.ToLower(s), "if not exists", "")
		return strings.Join(strings.Fields(s), "")
	}
	for _, statement := range strings.Split(initialSQL, ";") {
		fields := strings.Fields(statement)
		if len(fields) == 0 {
			continue
		}
		name := strings.Split(fields[2], "(")[0]
		var actual string
		if err := tx.QueryRow("SELECT sql FROM sqlite_master WHERE name=?", name).Scan(&actual); err != nil {
			return fmt.Errorf("incomplete database schema: %s: %w", name, err)
		}
		if normalize(actual) != normalize(statement) {
			return fmt.Errorf("unexpected database schema: %s", name)
		}
	}
	return nil
}
