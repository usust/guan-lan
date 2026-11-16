package competition

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type KnowledgePoint struct {
	Owner         string   `json:"owner_type"`
	Name          string   `json:"name"`
	Parent        string   `json:"parent_name"`
	Types         []string `json:"types"`
	QuestionCount int      `json:"question_count"`
	UsageCount    int      `json:"usage_count"`
}

func (s *Service) KnowledgePoints(ctx context.Context) ([]KnowledgePoint, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `WITH counts AS (
 SELECT j.value COLLATE NOCASE AS name,count(DISTINCT c.id) AS usage_count,
 count(DISTINCT CASE WHEN l.set_id IS NOT NULL THEN c.id END) AS question_count
 FROM challenges c LEFT JOIN question_library l ON l.set_id=c.set_id
 JOIN json_each(c.tags_json) j GROUP BY j.value COLLATE NOCASE
 ) SELECT k.name,COALESCE(k.parent_name,''),COALESCE(k.owner_type,''),COALESCE(n.question_count,0),COALESCE(n.usage_count,0)
 FROM knowledge_points k LEFT JOIN counts n ON n.name=k.name COLLATE NOCASE ORDER BY k.name`)
	if err != nil {
		return nil, err
	}
	items := []KnowledgePoint{}
	indices := map[string]int{}
	for rows.Next() {
		var p KnowledgePoint
		p.Types = []string{}
		if err = rows.Scan(&p.Name, &p.Parent, &p.Owner, &p.QuestionCount, &p.UsageCount); err != nil {
			rows.Close()
			return nil, err
		}
		indices[p.Name] = len(items)
		items = append(items, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, "SELECT point_name,type_name FROM knowledge_point_types ORDER BY type_name")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var point, category string
		if err = rows.Scan(&point, &category); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := indices[point]; ok {
			items[i].Types = append(items[i].Types, category)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Owner != "" {
			items[i].Types = []string{items[i].Owner}
		}
	}
	return items, tx.Commit()
}
func checkKnowledgeParent(ctx context.Context, tx *sql.Tx, source, parent string) error {
	seen := map[string]bool{}
	for parent != "" {
		if strings.EqualFold(source, parent) || seen[parent] {
			return invalid("不能选择自身或下级知识点")
		}
		seen[parent] = true
		var next sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT parent_name FROM knowledge_points WHERE name=?", parent).Scan(&next); err != nil {
			return invalid("上级知识点不存在")
		}
		parent = next.String
	}
	return nil
}

// Creating and moving never modifies question content; links are recommendations.
func (s *Service) SetKnowledge(ctx context.Context, actor, name, parent string, create bool, requestedOwner ...string) error {
	name = strings.TrimSpace(name)
	parent = strings.TrimSpace(parent)
	if !ValidTaxonomyName(name, false) {
		return invalid("知识点名称须为 1 至 32 个字符")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkKnowledgeParent(ctx, tx, name, parent); err != nil {
		return err
	}
	var parentValue any
	if parent != "" {
		if err = tx.QueryRowContext(ctx, "SELECT name FROM knowledge_points WHERE name=?", parent).Scan(&parent); err != nil {
			return err
		}
		parentValue = parent
	}
	owner := ""
	if len(requestedOwner) > 0 {
		owner = strings.TrimSpace(requestedOwner[0])
	}
	if parent != "" {
		var inherited sql.NullString
		if err = tx.QueryRowContext(ctx, "SELECT owner_type FROM knowledge_points WHERE name=?", parent).Scan(&inherited); err != nil {
			return err
		}
		if inherited.String == "" {
			return invalid("请先设置上级知识点的归属题型")
		}
		if owner != "" && !strings.EqualFold(owner, inherited.String) {
			return invalid("子知识点必须继承上级的归属题型")
		}
		owner = inherited.String
	} else if !create && owner == "" {
		var prior sql.NullString
		if err = tx.QueryRowContext(ctx, "SELECT owner_type FROM knowledge_points WHERE name=?", name).Scan(&prior); err != nil {
			return err
		}
		owner = prior.String
	}
	if owner == "" {
		return invalid("请选择知识点的归属题型")
	}
	if err = tx.QueryRowContext(ctx, "SELECT name FROM question_types WHERE name=?", owner).Scan(&owner); err != nil {
		return invalid("归属题型不存在")
	}
	if create {
		r, e := tx.ExecContext(ctx, "INSERT OR IGNORE INTO knowledge_points(name,parent_name,owner_type) VALUES(?,?,?)", name, parentValue, owner)
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n == 0 {
			return invalid("该知识点已存在，请使用已有节点")
		}
	} else {
		r, e := tx.ExecContext(ctx, "UPDATE knowledge_points SET parent_name=? WHERE name=?", parentValue, name)
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n == 0 {
			return sql.ErrNoRows
		}
	}
	if err = assignKnowledgeOwner(ctx, tx, name, owner); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "knowledge.structure", name+" -> "+parent); err != nil {
		return err
	}
	return tx.Commit()
}

// assignKnowledgeOwner moves ownership of an entire subtree atomically.
func assignKnowledgeOwner(ctx context.Context, tx *sql.Tx, name, owner string) error {
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE subtree(name) AS (SELECT name FROM knowledge_points WHERE name=? UNION ALL SELECT k.name FROM knowledge_points k JOIN subtree s ON k.parent_name=s.name) SELECT name FROM subtree`, name)
	if err != nil {
		return err
	}
	names := []string{}
	for rows.Next() {
		var n string
		if err = rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		names = append(names, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, n := range names {
		if _, err = tx.ExecContext(ctx, "UPDATE knowledge_points SET owner_type=? WHERE name=?", owner, n); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM knowledge_point_types WHERE point_name=?", n); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO knowledge_point_types(point_name,type_name) VALUES(?,?)", n, owner); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) LinkKnowledgeTypes(ctx context.Context, actor, name string, types []string) error {
	if len(types) != 1 {
		return invalid("每个知识点必须归属一个题型")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT name FROM knowledge_points WHERE name=?", name).Scan(&name); err != nil {
		return err
	}
	owner := types[0]
	if err = tx.QueryRowContext(ctx, "SELECT name FROM question_types WHERE name=?", owner).Scan(&owner); err != nil {
		return invalid("归属题型不存在")
	}
	// A subtree assigned elsewhere becomes a root under its new owning type.
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_points SET parent_name=NULL WHERE name=? AND parent_name IS NOT NULL AND NOT EXISTS(SELECT 1 FROM knowledge_points p WHERE p.name=knowledge_points.parent_name AND p.owner_type=?)`, name, owner); err != nil {
		return err
	}
	if err = assignKnowledgeOwner(ctx, tx, name, owner); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "knowledge.owner", name+" -> "+owner); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateLibraryKnowledge edits only metadata and preserves flags, packages and tasks.
func (s *Service) UpdateLibraryKnowledge(ctx context.Context, actor, id string, version int, tags []string) (int, error) {
	if len(tags) > 20 {
		return 0, invalid("每道题最多选择 20 个知识点")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var current int
	var status string
	if err = tx.QueryRowContext(ctx, "SELECT version,status FROM challenge_sets s JOIN question_library l ON l.set_id=s.id WHERE s.id=?", id).Scan(&current, &status); err != nil {
		return 0, err
	}
	if current != version || status != "DRAFT" {
		return 0, ErrConflict
	}
	canonical := []string{}
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if err = tx.QueryRowContext(ctx, "SELECT name FROM knowledge_points WHERE name=?", tag).Scan(&tag); err != nil {
			return 0, invalid("所选知识点不存在，请刷新后重试")
		}
		if !seen[tag] {
			canonical = append(canonical, tag)
			seen[tag] = true
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE challenges SET tags_json=? WHERE set_id=?", tagsJSON(canonical), id); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE challenge_sets SET version=version+1,updated_at=? WHERE id=?", time.Now().UnixMilli(), id); err != nil {
		return 0, err
	}
	if err = audit(ctx, tx, actor, "question.knowledge", id); err != nil {
		return 0, err
	}
	return current + 1, tx.Commit()
}
