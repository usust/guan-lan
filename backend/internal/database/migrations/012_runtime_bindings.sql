CREATE TABLE runtime_bindings (
 user_id TEXT NOT NULL,
 target_id TEXT NOT NULL,
 instance_id TEXT NOT NULL REFERENCES runtime_instances(id) ON DELETE CASCADE,
 PRIMARY KEY(user_id,target_id)
);
CREATE INDEX runtime_bindings_instance ON runtime_bindings(instance_id);
