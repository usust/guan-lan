CREATE TABLE subtask_resources (
 subtask_id TEXT PRIMARY KEY REFERENCES subtasks(id) ON DELETE CASCADE,
 attachments_json TEXT NOT NULL, service_keys_json TEXT NOT NULL
);
