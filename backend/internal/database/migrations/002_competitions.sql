CREATE TABLE challenge_sets (
 id TEXT PRIMARY KEY, title TEXT NOT NULL, description TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('DRAFT','PUBLISHED','CLOSED')),
 starts_at INTEGER NOT NULL, ends_at INTEGER NOT NULL CHECK(ends_at>starts_at),
 version INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE challenges (
 id TEXT PRIMARY KEY, set_id TEXT NOT NULL REFERENCES challenge_sets(id) ON DELETE CASCADE,
 title TEXT NOT NULL, description TEXT NOT NULL, category TEXT NOT NULL, difficulty TEXT NOT NULL,
 position INTEGER NOT NULL, environment_json TEXT NOT NULL, package_path TEXT NOT NULL DEFAULT '', package_sha256 TEXT NOT NULL DEFAULT ''
);
CREATE INDEX challenges_set ON challenges(set_id,position);
CREATE TABLE subtasks (
 id TEXT PRIMARY KEY, challenge_id TEXT NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
 title TEXT NOT NULL, content TEXT NOT NULL, points INTEGER NOT NULL CHECK(points>0),
 service_key TEXT NOT NULL, flag_hash TEXT NOT NULL, position INTEGER NOT NULL
);
CREATE INDEX subtasks_challenge ON subtasks(challenge_id,position);
CREATE TABLE submissions (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id),
 subtask_id TEXT NOT NULL REFERENCES subtasks(id), correct INTEGER NOT NULL CHECK(correct IN (0,1)),
 created_at INTEGER NOT NULL
);
CREATE INDEX submissions_rate ON submissions(user_id,created_at);
CREATE TABLE solves (
 user_id TEXT NOT NULL REFERENCES users(id), subtask_id TEXT NOT NULL REFERENCES subtasks(id),
 submission_id TEXT NOT NULL UNIQUE REFERENCES submissions(id), points INTEGER NOT NULL,
 solved_at INTEGER NOT NULL, PRIMARY KEY(user_id,subtask_id)
);
INSERT INTO metadata(key,value) VALUES('flag_hmac_key',lower(hex(randomblob(32))));
