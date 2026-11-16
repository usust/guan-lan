CREATE TABLE question_types (name TEXT PRIMARY KEY COLLATE NOCASE);
INSERT INTO question_types(name) VALUES ('WEB'),('PWN'),('逆向'),('密码学'),('MISC'),('数字取证'),('移动安全');
INSERT OR IGNORE INTO question_types(name) SELECT DISTINCT trim(category) FROM challenges WHERE trim(category) <> '';
UPDATE challenges SET category=(SELECT name FROM question_types WHERE name=trim(challenges.category)) WHERE trim(category) <> '';
ALTER TABLE challenges ADD COLUMN tags_json TEXT NOT NULL DEFAULT '[]';
CREATE INDEX challenges_category ON challenges(category);
