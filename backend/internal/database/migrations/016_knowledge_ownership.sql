ALTER TABLE knowledge_points ADD COLUMN owner_type TEXT COLLATE NOCASE REFERENCES question_types(name) ON UPDATE CASCADE ON DELETE RESTRICT;
CREATE INDEX knowledge_points_owner ON knowledge_points(owner_type);
-- Assign only unambiguous trees. Preserve old links when a tree needs review.
WITH RECURSIVE members(root,name) AS (
 SELECT name,name FROM knowledge_points WHERE parent_name IS NULL
 UNION ALL SELECT m.root,k.name FROM knowledge_points k JOIN members m ON k.parent_name=m.name
), owners AS (
 SELECT m.root,min(l.type_name) AS owner FROM members m JOIN knowledge_point_types l ON l.point_name=m.name
 GROUP BY m.root HAVING count(DISTINCT l.type_name)=1
)
UPDATE knowledge_points SET owner_type=(SELECT o.owner FROM owners o JOIN members m ON m.root=o.root WHERE m.name=knowledge_points.name);
