CREATE TABLE difficulties (
 code TEXT PRIMARY KEY,
 name TEXT NOT NULL COLLATE NOCASE UNIQUE,
 position INTEGER NOT NULL
);
INSERT INTO difficulties VALUES ('EASY','简单',0),('MEDIUM','中等',1),('HARD','困难',2);
