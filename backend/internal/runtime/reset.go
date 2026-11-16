package runtime

import "context"

// Reset holds the runtime lock through data cleanup, excluding reconciliation.
func (m *Manager) Reset(ctx context.Context, clear func() error) error {
	if err := m.lock(); err != nil {
		return err
	}
	defer m.unlock()
	rows, err := m.DB.QueryContext(ctx, "SELECT id FROM runtime_instances")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = m.cleanup(ctx, id); err != nil {
			return err
		}
	}
	return clear()
}
