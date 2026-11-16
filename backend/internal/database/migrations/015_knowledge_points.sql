CREATE TABLE knowledge_points (
 name TEXT PRIMARY KEY COLLATE NOCASE,
 parent_name TEXT COLLATE NOCASE REFERENCES knowledge_points(name) ON UPDATE CASCADE ON DELETE RESTRICT
);
CREATE INDEX knowledge_points_parent ON knowledge_points(parent_name);
CREATE TABLE knowledge_point_types (
 point_name TEXT NOT NULL COLLATE NOCASE REFERENCES knowledge_points(name) ON UPDATE CASCADE ON DELETE CASCADE,
 type_name TEXT NOT NULL COLLATE NOCASE REFERENCES question_types(name) ON UPDATE CASCADE ON DELETE CASCADE,
 PRIMARY KEY(point_name,type_name)
);
INSERT OR IGNORE INTO knowledge_points(name) SELECT DISTINCT trim(j.value) FROM challenges c,json_each(c.tags_json) j WHERE j.type='text' AND trim(j.value)<>'';
