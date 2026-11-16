CREATE TABLE staged_packages (
    token TEXT PRIMARY KEY,
    actor_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    environment_json TEXT NOT NULL,
    package_path TEXT NOT NULL UNIQUE,
    package_sha256 TEXT NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX staged_packages_expiry ON staged_packages(expires_at);
