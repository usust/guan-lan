package competition

import (
	"context"
	"database/sql"
	"strings"
)

// Walking ancestors also rejects merging a parent into any of its descendants.
func checkTypeParent(ctx context.Context, tx *sql.Tx, source, parent string) error {
	seen := map[string]bool{}
	for parent != "" {
		if strings.EqualFold(parent, source) || seen[parent] {
			return invalid("不能选择自身或下级题型，否则会形成循环")
		}
		seen[parent] = true
		var next sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT parent_name FROM question_types WHERE name=?", parent).Scan(&next); err != nil {
			return invalid("上级题型不存在，请刷新后重试")
		}
		parent = next.String
	}
	return nil
}

func (s *Service) MoveQuestionType(ctx context.Context, actor, name, parent string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT name FROM question_types WHERE name=?", name).Scan(&name); err != nil {
		return err
	}
	parent = strings.TrimSpace(parent)
	if err = checkTypeParent(ctx, tx, name, parent); err != nil {
		return err
	}
	var value any
	if parent != "" {
		if err = tx.QueryRowContext(ctx, "SELECT name FROM question_types WHERE name=?", parent).Scan(&parent); err != nil {
			return err
		}
		value = parent
	}
	if _, err = tx.ExecContext(ctx, "UPDATE question_types SET parent_name=? WHERE name=?", value, name); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "taxonomy.type.move", name+" -> "+parent); err != nil {
		return err
	}
	return tx.Commit()
}
