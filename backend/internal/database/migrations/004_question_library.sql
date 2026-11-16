CREATE TABLE question_library (
 set_id TEXT PRIMARY KEY REFERENCES challenge_sets(id) ON DELETE CASCADE
);
