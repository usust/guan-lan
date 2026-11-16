package database

import (
	"database/sql"
	_ "modernc.org/sqlite"
	"net/url"
)

func Open(path, platformID string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite", u.String()+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = migrate(db, platformID, []migration{{sql: initialSQL}, {sql: competitionsSQL}, {sql: runtimeSQL}, {sql: questionLibrarySQL}, {sql: attachmentsSQL}, {sql: stagedPackagesSQL}, {sql: packageUploadsSQL}, {sql: subtaskResourcesSQL}, {sql: questionSourcesSQL}, {sql: solutionDocumentsSQL}, {sql: attachmentUploadsSQL}, {sql: runtimeBindingsSQL}, {sql: questionTaxonomySQL}, {sql: questionTypeHierarchySQL}, {sql: knowledgePointsSQL}, {sql: knowledgeOwnershipSQL}, {sql: difficultiesSQL}}); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
