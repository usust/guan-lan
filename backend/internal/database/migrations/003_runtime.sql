CREATE TABLE runtime_verifications (
 challenge_id TEXT PRIMARY KEY REFERENCES challenges(id) ON DELETE CASCADE,
 package_sha256 TEXT NOT NULL, environment_json TEXT NOT NULL,
 engine_id TEXT NOT NULL, verified_at INTEGER NOT NULL
);
CREATE TABLE runtime_instances (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, challenge_id TEXT NOT NULL,
 state TEXT NOT NULL, details TEXT NOT NULL DEFAULT '{}', expires_at INTEGER NOT NULL,
 UNIQUE(user_id,challenge_id)
);
