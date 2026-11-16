package competition

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

func ValidTaxonomyName(name string, category bool) bool {
	return name != "" && name == strings.TrimSpace(name) && utf8.RuneCountInString(name) <= 32 && (!category || len(name) <= 64) && !strings.ContainsAny(name, "\r\n\t")
}

type TaxonomyChange struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Mode   string `json:"mode"`
}

// Taxonomy maintenance only changes classification metadata, including competition
// copies. Every affected set gets a new version so stale editors cannot undo it.
func (s *Service) ChangeTaxonomy(ctx context.Context, actor, kind string, change TaxonomyChange) (int, error) {
	change.Source = strings.TrimSpace(change.Source)
	change.Target = strings.TrimSpace(change.Target)
	if (kind != "type" && kind != "tag") || !ValidTaxonomyName(change.Source, kind == "type") || (change.Mode != "rename" && change.Mode != "merge" && change.Mode != "delete") || (change.Mode != "delete" && !ValidTaxonomyName(change.Target, kind == "type")) || (kind == "type" && change.Mode == "delete") {
		return 0, invalid("整理方式或名称无效")
	}
	if change.Source == change.Target {
		return 0, invalid("源名称和目标名称不能相同")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if kind == "type" {
		if err = tx.QueryRowContext(ctx, "SELECT name FROM question_types WHERE name=?", change.Source).Scan(&change.Source); err != nil {
			return 0, err
		}
		var existing string
		e := tx.QueryRowContext(ctx, "SELECT name FROM question_types WHERE name=?", change.Target).Scan(&existing)
		if e != nil && e != sql.ErrNoRows {
			return 0, e
		}
		if change.Mode == "merge" {
			if e != nil {
				return 0, invalid("请选择已存在的目标题型")
			}
			if strings.EqualFold(existing, change.Source) {
				return 0, invalid("不能合并到自身")
			}
			change.Target = existing
			if err = checkTypeParent(ctx, tx, change.Source, change.Target); err != nil {
				return 0, err
			}
		} else {
			if e == nil && !strings.EqualFold(existing, change.Source) {
				return 0, invalid("目标题型已存在，请使用合并")
			}
			if _, err = tx.ExecContext(ctx, "UPDATE question_types SET name=? WHERE name=?", change.Target, change.Source); err != nil {
				return 0, err
			}
			// SQLite NOCASE considers case-only renames equal and may skip the
			// FK cascade; keep the stored parent spelling canonical explicitly.
			if _, err = tx.ExecContext(ctx, "UPDATE question_types SET parent_name=? WHERE parent_name=?", change.Target, change.Source); err != nil {
				return 0, err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE knowledge_point_types SET type_name=? WHERE type_name=?", change.Target, change.Source); err != nil {
				return 0, err
			}

		}
	}
	if kind == "tag" {
		if err = tx.QueryRowContext(ctx, "SELECT name FROM knowledge_points WHERE name=?", change.Source).Scan(&change.Source); err != nil {
			return 0, err
		}
		var target string
		e := tx.QueryRowContext(ctx, "SELECT name FROM knowledge_points WHERE name=?", change.Target).Scan(&target)
		if e != nil && e != sql.ErrNoRows {
			return 0, e
		}
		if change.Mode == "merge" {
			if e != nil {
				return 0, invalid("目标知识点不存在")
			}
			change.Target = target
			if err = checkKnowledgeParent(ctx, tx, change.Source, change.Target); err != nil {
				return 0, err
			}
		} else if change.Mode == "rename" {
			if e == nil && !strings.EqualFold(target, change.Source) {
				return 0, invalid("目标知识点已存在，请使用合并")
			}
			if _, err = tx.ExecContext(ctx, "UPDATE knowledge_points SET name=? WHERE name=?", change.Target, change.Source); err != nil {
				return 0, err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE knowledge_points SET parent_name=? WHERE parent_name=?", change.Target, change.Source); err != nil {
				return 0, err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE knowledge_point_types SET point_name=? WHERE point_name=?", change.Target, change.Source); err != nil {
				return 0, err
			}

		} else {
			var children int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM knowledge_points WHERE parent_name=?", change.Source).Scan(&children); err != nil {
				return 0, err
			}
			if children > 0 {
				return 0, invalid("请先移动或合并下级知识点")
			}
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,set_id,category,tags_json FROM challenges")
	if err != nil {
		return 0, err
	}
	type update struct {
		id, set, category string
		tags              []string
	}
	updates := []update{}
	for rows.Next() {
		var u update
		var raw string
		if err = rows.Scan(&u.id, &u.set, &u.category, &raw); err != nil {
			rows.Close()
			return 0, err
		}
		if err = json.Unmarshal([]byte(raw), &u.tags); err != nil {
			rows.Close()
			return 0, err
		}
		changed := false
		if kind == "type" {
			if strings.EqualFold(u.category, change.Source) {
				u.category = change.Target
				changed = true
			}
		} else {
			tags := []string{}
			seen := map[string]bool{}
			for _, tag := range u.tags {
				if strings.EqualFold(tag, change.Source) {
					changed = true
					if change.Mode == "delete" {
						continue
					}
					tag = change.Target
				}
				// Prefer the requested canonical spelling when merging case variants.
				if change.Mode != "delete" && strings.EqualFold(tag, change.Target) {
					tag = change.Target
				}
				key := strings.ToLower(tag)
				if !seen[key] {
					tags = append(tags, tag)
					seen[key] = true
				}
			}
			u.tags = tags
		}
		if changed {
			updates = append(updates, u)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	sets := map[string]bool{}
	for _, u := range updates {
		if _, err = tx.ExecContext(ctx, "UPDATE challenges SET category=?,tags_json=? WHERE id=?", u.category, tagsJSON(u.tags), u.id); err != nil {
			return 0, err
		}
		sets[u.set] = true
	}
	for id := range sets {
		if _, err = tx.ExecContext(ctx, "UPDATE challenge_sets SET version=version+1,updated_at=? WHERE id=?", time.Now().UnixMilli(), id); err != nil {
			return 0, err
		}
	}
	if kind == "type" && change.Mode == "rename" {
		if _, err = tx.ExecContext(ctx, "UPDATE knowledge_points SET owner_type=? WHERE owner_type=?", change.Target, change.Source); err != nil {
			return 0, err
		}
	}
	if kind == "type" && change.Mode == "merge" {
		if _, err = tx.ExecContext(ctx, "UPDATE knowledge_points SET owner_type=? WHERE owner_type=?", change.Target, change.Source); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE question_types SET parent_name=? WHERE parent_name=?", change.Target, change.Source); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO knowledge_point_types(point_name,type_name) SELECT point_name,? FROM knowledge_point_types WHERE type_name=?", change.Target, change.Source); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM question_types WHERE name=?", change.Source); err != nil {
			return 0, err
		}
	}
	if kind == "tag" && change.Mode == "merge" {
		var owner sql.NullString
		if err = tx.QueryRowContext(ctx, "SELECT owner_type FROM knowledge_points WHERE name=?", change.Target).Scan(&owner); err != nil {
			return 0, err
		}
		if owner.String == "" {
			return 0, invalid("请先设置目标知识点的归属题型")
		}
		if err = assignKnowledgeOwner(ctx, tx, change.Source, owner.String); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE knowledge_points SET parent_name=? WHERE parent_name=?", change.Target, change.Source); err != nil {
			return 0, err
		}
	}
	if kind == "tag" && (change.Mode == "merge" || change.Mode == "delete") {
		if _, err = tx.ExecContext(ctx, "DELETE FROM knowledge_points WHERE name=?", change.Source); err != nil {
			return 0, err
		}
	}
	if err = audit(ctx, tx, actor, "taxonomy."+kind+"."+change.Mode, change.Source+" -> "+change.Target); err != nil {
		return 0, err
	}
	return len(updates), tx.Commit()
}

type LibrarySelection struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

func (s *Service) ReclassifyLibrary(ctx context.Context, actor, category string, items []LibrarySelection) (int, error) {
	if len(items) == 0 || len(items) > 500 {
		return 0, invalid("请选择 1 至 500 道题目")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT name FROM question_types WHERE name=?", category).Scan(&category); err != nil {
		return 0, invalid("目标题型不存在，请刷新后重试")
	}
	seen := map[string]bool{}
	count := 0
	for _, item := range items {
		if seen[item.ID] {
			return 0, invalid("题目选择重复")
		}
		seen[item.ID] = true
		var version int
		var status string
		if err = tx.QueryRowContext(ctx, "SELECT version,status FROM challenge_sets s JOIN question_library l ON l.set_id=s.id WHERE s.id=?", item.ID).Scan(&version, &status); err != nil {
			return 0, ErrConflict
		}
		if item.Version != version || status != "DRAFT" {
			return 0, ErrConflict
		}
		result, e := tx.ExecContext(ctx, "UPDATE challenges SET category=? WHERE set_id=? AND category<>?", category, item.ID, category)
		if e != nil {
			return 0, e
		}
		n, _ := result.RowsAffected()
		if n > 0 {
			count += int(n)
			if _, err = tx.ExecContext(ctx, "UPDATE challenge_sets SET version=version+1,updated_at=? WHERE id=?", time.Now().UnixMilli(), item.ID); err != nil {
				return 0, err
			}
		}
	}
	if err = audit(ctx, tx, actor, "taxonomy.type.batch", category); err != nil {
		return 0, err
	}
	return count, tx.Commit()
}
