CREATE TABLE question_sources (
 challenge_id TEXT PRIMARY KEY REFERENCES challenges(id) ON DELETE CASCADE,
 source_id TEXT NOT NULL
);
CREATE TABLE subtask_sources (
 subtask_id TEXT PRIMARY KEY REFERENCES subtasks(id) ON DELETE CASCADE,
 source_id TEXT NOT NULL
);
-- Recover legacy copies only when one library question matches their structure.
INSERT INTO question_sources
SELECT q.id, min(src.id) FROM challenges q
JOIN challenges src ON src.title=q.title AND src.category=q.category AND src.difficulty=q.difficulty
JOIN question_library l ON l.set_id=src.set_id
WHERE NOT EXISTS (SELECT 1 FROM question_library WHERE set_id=q.set_id)
AND (SELECT count(*) FROM subtasks WHERE challenge_id=q.id)=(SELECT count(*) FROM subtasks WHERE challenge_id=src.id)
AND NOT EXISTS (SELECT 1 FROM subtasks t WHERE t.challenge_id=q.id AND NOT EXISTS
 (SELECT 1 FROM subtasks st WHERE st.challenge_id=src.id AND st.position=t.position AND st.title=t.title))
GROUP BY q.id HAVING count(*)=1;
INSERT INTO subtask_sources
SELECT t.id,st.id FROM question_sources qs JOIN subtasks t ON t.challenge_id=qs.challenge_id
JOIN subtasks st ON st.challenge_id=qs.source_id AND st.position=t.position AND st.title=t.title;
