package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"huashan/internal/environment"
)

// Status only exposes environments explicitly started for this subtask. Merely
// reading a related subtask must not activate it or discover reusable instances.
func (m *Manager) Status(ctx context.Context, user, target string) (Instance, error) {
	x, err := m.instanceStatus(ctx, user, target)
	if err != nil || x.ID != "" {
		return x, err
	}
	var source string
	err = m.DB.QueryRowContext(ctx, `SELECT r.challenge_id FROM runtime_bindings b JOIN runtime_instances r ON r.id=b.instance_id AND r.user_id=b.user_id WHERE b.user_id=? AND b.target_id=?`, user, target).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		return x, nil
	}
	if err != nil {
		return x, err
	}
	x, err = m.instanceStatus(ctx, user, source)
	if err != nil {
		return x, err
	}
	_, wanted, err := m.targetServices(ctx, target)
	if err != nil {
		return x, err
	}
	x.Endpoints = scopedEndpoints(x.Endpoints, wanted)
	return x, nil
}

func (m *Manager) bindInstance(ctx context.Context, user, target string, x Instance) (Instance, error) {
	_, err := m.DB.ExecContext(ctx, `INSERT INTO runtime_bindings(user_id,target_id,instance_id) VALUES(?,?,?) ON CONFLICT(user_id,target_id) DO UPDATE SET instance_id=excluded.instance_id`, user, target, x.ID)
	return x, err
}

func scopedEndpoints(endpoints []Endpoint, wanted environment.Manifest) []Endpoint {
	allowed := map[string]bool{}
	for _, service := range wanted.Services {
		allowed[service.Key] = true
	}
	result := []Endpoint{}
	for _, endpoint := range endpoints {
		if allowed[endpoint.Service] {
			result = append(result, endpoint)
		}
	}
	return result
}

func (m *Manager) targetServices(ctx context.Context, target string) (string, environment.Manifest, error) {
	parent, keys, err := m.runtimeTarget(ctx, target)
	if err != nil {
		return "", environment.Manifest{}, err
	}
	var raw string
	if err = m.DB.QueryRowContext(ctx, "SELECT environment_json FROM challenges WHERE id=?", parent).Scan(&raw); err != nil {
		return "", environment.Manifest{}, err
	}
	var manifest environment.Manifest
	if err = json.Unmarshal([]byte(raw), &manifest); err != nil {
		return "", manifest, err
	}
	manifest, err = scopedManifest(manifest, keys)
	return parent, manifest, err
}

func (m *Manager) reusableInstance(ctx context.Context, user, target string) (Instance, error) {
	stopped := Instance{State: "STOPPED", Endpoints: []Endpoint{}}
	parent, wanted, err := m.targetServices(ctx, target)
	if err != nil {
		return stopped, err
	}
	rows, err := m.DB.QueryContext(ctx, `SELECT r.challenge_id FROM runtime_instances r LEFT JOIN subtasks t ON t.id=r.challenge_id WHERE r.user_id=? AND COALESCE(t.challenge_id,r.challenge_id)=? AND r.challenge_id!=? AND r.state='RUNNING' AND r.expires_at>? ORDER BY r.expires_at DESC,r.id`, user, parent, target, time.Now().UnixMilli())
	if err != nil {
		return stopped, err
	}
	var targets []string
	for rows.Next() {
		var source string
		if err = rows.Scan(&source); err != nil {
			rows.Close()
			return stopped, err
		}
		targets = append(targets, source)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return stopped, err
	}
	type candidate struct {
		target string
		size   int
	}
	var candidates []candidate
	for _, source := range targets {
		_, available, err := m.targetServices(ctx, source)
		if err != nil {
			return stopped, err
		}
		if coversServices(available, wanted) {
			candidates = append(candidates, candidate{source, len(available.Services)})
		}
	}
	// Prefer the smallest complete group, retaining the expiry ordering for ties.
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].size < candidates[j].size })
	for _, candidate := range candidates {
		x, err := m.instanceStatus(ctx, user, candidate.target)
		if err != nil {
			return stopped, err
		}
		if x.State != "RUNNING" {
			continue
		}
		x.Endpoints = scopedEndpoints(x.Endpoints, wanted)
		return x, nil
	}
	return stopped, nil
}

func coversServices(available, wanted environment.Manifest) bool {
	if len(wanted.Services) == 0 {
		return false
	}
	keys := map[string]bool{}
	for _, service := range available.Services {
		keys[service.Key] = true
	}
	for _, service := range wanted.Services {
		if !keys[service.Key] {
			return false
		}
	}
	return true
}
