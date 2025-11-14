package state

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"boundless_scraper/internal/filesystem"
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

// Load reads the previous state from disk, if present.
func Load(path string) (State, error) {
	var s State

	expanded, err := filesystem.ExpandPath(path)
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

// Save writes the current scraper state to disk safely.
func Save(path string, s State) error {
	expanded, err := filesystem.ExpandPath(path)
	if err != nil {
		return err
	}

	log.Printf("Saving state to %s", expanded)

	if err := os.MkdirAll(filepath.Dir(expanded), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
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

// FormatTimestamp formats a time.Time for saving to the state file.
func FormatTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
