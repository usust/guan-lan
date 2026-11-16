ALTER TABLE question_types ADD COLUMN parent_name TEXT COLLATE NOCASE REFERENCES question_types(name) ON UPDATE CASCADE ON DELETE RESTRICT;
CREATE INDEX question_types_parent ON question_types(parent_name);
