CREATE TABLE package_uploads (
    id TEXT PRIMARY KEY,
    actor_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    fingerprint TEXT NOT NULL,
    name TEXT NOT NULL,
    size INTEGER NOT NULL CHECK(size > 0),
    offset INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL DEFAULT 'uploading',
    error TEXT NOT NULL DEFAULT '',
    result_json TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL
);
CREATE INDEX package_uploads_owner ON package_uploads(actor_id, fingerprint);
CREATE TABLE package_upload_chunks (
    upload_id TEXT NOT NULL REFERENCES package_uploads(id) ON DELETE CASCADE,
    offset INTEGER NOT NULL,
    size INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    PRIMARY KEY(upload_id, offset)
);
