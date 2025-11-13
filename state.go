package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"
)

// State holds the last processed timestamp for incremental scraping.
type State struct {
	LastTimestamp string  `json:"last_timestamp"`
	TotalOrders   int     `json:"total_orders"`
	TotalCycles   float64 `json:"total_cycles"`
}

// Timestamp returns the saved timestamp, if available.
func (s State) Timestamp() (time.Time, bool) {
	if s.LastTimestamp == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, s.LastTimestamp)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

// loadState loads the previous state from disk, if present.
func loadState(path string) (State, error) {
	var s State

	expanded, err := expandPath(path)
	if err != nil {
		return s, err
	}

	data, err := os.ReadFile(expanded)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("No existing state file at %s", expanded)
			return s, nil
		}
		return s, err
	}

	if err := json.Unmarshal(data, &s); err != nil {
		return s, err
	}

	log.Printf("Loaded state from %s: %+v", expanded, s)
	return s, nil
}

// saveState writes the current scraper state to disk safely.
func saveState(path string, state State) error {
	expanded, err := expandPath(path)
	if err != nil {
		return err
	}

	// Always log where we're writing, to help debug path issues.
	log.Printf("Saving state to %s", expanded)

	if err := os.MkdirAll(filepath.Dir(expanded), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	tmp := expanded + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, expanded); err != nil {
		return err
	}

	log.Printf("Successfully saved state to %s", expanded)
	return nil
}

// formatTimestamp formats a time.Time for saving to the state file.
func formatTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
