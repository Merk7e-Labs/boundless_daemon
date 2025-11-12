package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type State struct {
	LastTimestamp string `json:"last_timestamp"`
}

func loadState(path string) (State, error) {
	var state State
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, err
	}
	if len(data) == 0 {
		return State{}, nil
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	return state, nil
}

func saveState(path string, state State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s State) Timestamp() (time.Time, bool) {
	if s.LastTimestamp == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339Nano, s.LastTimestamp); err == nil {
		return ts, true
	}
	if ts, err := time.Parse(time.RFC3339, s.LastTimestamp); err == nil {
		return ts, true
	}
	return time.Time{}, false
}

func formatTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(time.RFC3339Nano)
}
