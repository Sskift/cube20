package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type roundRobinState struct {
	LastAccountID string `json:"lastAccountId"`
}

func (m *Manager) ResetRoundRobin() error {
	if err := m.Ensure(); err != nil {
		return err
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		db, err := m.postgresDB(ctx)
		if err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `DELETE FROM cube_meta WHERE key = 'round_robin_last_account_id'`)
		return err
	}
	err := os.Remove(filepath.Join(m.StateDir, roundRobinFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func (m *Manager) loadRoundRobinState() (roundRobinState, error) {
	if err := m.Ensure(); err != nil {
		return roundRobinState{}, err
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		db, err := m.postgresDB(ctx)
		if err != nil {
			return roundRobinState{}, err
		}
		var value string
		err = db.QueryRowContext(ctx, `SELECT value FROM cube_meta WHERE key = 'round_robin_last_account_id'`).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			return roundRobinState{}, nil
		}
		if err != nil {
			return roundRobinState{}, err
		}
		return roundRobinState{LastAccountID: value}, nil
	}
	data, err := os.ReadFile(filepath.Join(m.StateDir, roundRobinFileName))
	if errors.Is(err, os.ErrNotExist) {
		return roundRobinState{}, nil
	}
	if err != nil {
		return roundRobinState{}, err
	}
	var state roundRobinState
	if err := json.Unmarshal(data, &state); err != nil {
		return roundRobinState{}, err
	}
	return state, nil
}
func (m *Manager) saveRoundRobinState(state roundRobinState) error {
	if err := m.Ensure(); err != nil {
		return err
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		db, err := m.postgresDB(ctx)
		if err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `INSERT INTO cube_meta (key, value, updated_at)
			VALUES ('round_robin_last_account_id', $1, now())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, state.LastAccountID)
		return err
	}
	path := filepath.Join(m.StateDir, roundRobinFileName)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
