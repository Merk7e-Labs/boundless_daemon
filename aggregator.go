package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

/*
Run:
  # Time-based (default) - finds RFC3339/Nano anywhere in the line:
  go run aggregator.go -file=./test.txt -prefix=0x5a1f4d397 -url=http://localhost:9001/report -state=./agg_state.json

  # Offset-based (fallback) - tail-like; ignores timestamps:
  go run aggregator.go -file=./test.txt -prefix=0x5a1f4d397 -url=http://localhost:9001/report -state=./agg_state.json -mode=offset

Flags:
  -file     : path to log file
  -prefix   : order id prefix to include (e.g., 0x5a1f4d397)
  -url      : mock server /report endpoint
  -state    : path to JSON state file
  -mode     : "time" (default) or "offset"
  -reset    : delete state and start from scratch

Behavior:
  - Time mode: parse RFC3339/RFC3339Nano timestamp anywhere in each line; process only lines with ts > last_read_at.
  - Offset mode: seek to last_offset and read forward only (tail-like).
  - Dedup across runs; cumulative count & IDs posted to mock server.
*/

var (
	reCompleted = regexp.MustCompile(`Completed order:\s*(0x[0-9a-fA-F]{9})([0-9a-fA-F]*)`)

	// RFC3339 or RFC3339Nano anywhere in line (e.g., 2025-11-07T09:55:00Z or ...00.123456789Z)
	// This is permissive and avoids false negatives when logs prepend/append metadata.
	reRFC3339Any = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z`)
)

type Mode string

const (
	ModeTime   Mode = "time"
	ModeOffset Mode = "offset"
)

type State struct {
	// Common
	OrderIDs   map[string]bool `json:"order_ids"`
	TotalCount int             `json:"total_count"`

	// Time-mode
	LastReadAt string `json:"last_read_at"` // RFC3339

	// Offset-mode
	LastOffset int64  `json:"last_offset"`
	FileKey    string `json:"file_key"` // to detect rotation (inode+dev hash)
}

func fileKey(info os.FileInfo) string {
	// Windows-safe fallback: hash file name + size + modtime
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%s:%d:%d", info.Name(), info.Size(), info.ModTime().UnixNano())
	return fmt.Sprintf("win:%x", h.Sum64())
}

func loadState(path string) (*State, error) {
	s := &State{OrderIDs: make(map[string]bool)}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, err
	}
	if s.OrderIDs == nil {
		s.OrderIDs = make(map[string]bool)
	}
	return s, nil
}

func saveStateAtomic(path string, st *State) error {
	tmp := path + ".tmp"
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func parseTimestampAnywhere(line string) (time.Time, bool) {
	loc := reRFC3339Any.FindStringIndex(line)
	if loc == nil {
		return time.Time{}, false
	}
	tsStr := line[loc[0]:loc[1]]
	// Try Nano first, then plain
	if ts, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
		return ts.UTC(), true
	}
	if ts, err := time.Parse(time.RFC3339, tsStr); err == nil {
		return ts.UTC(), true
	}
	return time.Time{}, false
}

func main() {
	path := flag.String("file", "./test.txt", "path to log file")
	prefix := flag.String("prefix", "0x5a1f4d397", "order id prefix to include")
	url := flag.String("url", "http://localhost:9001/report", "mock server report endpoint")
	statePath := flag.String("state", "./agg_state.json", "path to persistent state file")
	modeStr := flag.String("mode", "time", "incremental mode: time|offset")
	reset := flag.Bool("reset", false, "delete existing state and start from scratch")
	flag.Parse()

	mode := Mode(strings.ToLower(strings.TrimSpace(*modeStr)))
	if mode != ModeTime && mode != ModeOffset {
		log.Fatalf("unknown -mode=%q (use time|offset)", *modeStr)
	}

	if *reset {
		_ = os.Remove(*statePath)
	}

	state, err := loadState(*statePath)
	if err != nil {
		log.Fatalf("load state: %v", err)
	}

	want := strings.ToLower(strings.TrimSpace(*prefix))

	f, err := os.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		log.Fatal(err)
	}

	newIDs := 0

	switch mode {
	case ModeTime:
		var lastRead time.Time
		if state.LastReadAt != "" {
			if t, err := time.Parse(time.RFC3339, state.LastReadAt); err == nil {
				lastRead = t
			} else {
				log.Printf("warning: invalid LastReadAt in state (%q): %v", state.LastReadAt, err)
			}
		}

		sc := bufio.NewScanner(f)
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 2*1024*1024)

		maxSeen := lastRead
		for sc.Scan() {
			line := sc.Text()

			// timestamp can be anywhere in line
			ts, ok := parseTimestampAnywhere(line)
			if !ok {
				// If a line lacks a timestamp, we can’t safely order it; skip it.
				continue
			}
			if !ts.After(lastRead) {
				continue
			}
			if ts.After(maxSeen) {
				maxSeen = ts
			}

			m := reCompleted.FindStringSubmatch(line)
			if len(m) < 2 {
				continue
			}
			id := strings.ToLower(m[1] + m[2])
			if !strings.HasPrefix(id, want) {
				continue
			}
			if !state.OrderIDs[id] {
				state.OrderIDs[id] = true
				newIDs++
			}
		}
		if err := sc.Err(); err != nil {
			log.Fatal(err)
		}

		if maxSeen.After(lastRead) {
			state.LastReadAt = maxSeen.Format(time.RFC3339)
		}

	case ModeOffset:
		key := fileKey(fi)
		// If file rotated/truncated, reset offset
		if state.FileKey != key || state.LastOffset > fi.Size() {
			state.LastOffset = 0
			state.FileKey = key
		}

		if state.LastOffset > 0 {
			if _, err := f.Seek(state.LastOffset, 0); err != nil {
				log.Fatalf("seek: %v", err)
			}
		}

		sc := bufio.NewScanner(f)
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 2*1024*1024)

		readBytes := state.LastOffset
		for sc.Scan() {
			line := sc.Text()
			readBytes += int64(len(line)) + 1 // +1 for '\n'

			m := reCompleted.FindStringSubmatch(line)
			if len(m) < 2 {
				continue
			}
			id := strings.ToLower(m[1] + m[2])
			if !strings.HasPrefix(id, want) {
				continue
			}
			if !state.OrderIDs[id] {
				state.OrderIDs[id] = true
				newIDs++
			}
		}
		if err := sc.Err(); err != nil {
			log.Fatal(err)
		}
		state.LastOffset = readBytes
	}

	state.TotalCount = len(state.OrderIDs)

	// Save state
	if err := saveStateAtomic(*statePath, state); err != nil {
		log.Fatalf("save state: %v", err)
	}

	// Build cumulative payload (stable sort just for pretty diffs)
	orderIDs := make([]string, 0, len(state.OrderIDs))
	for id := range state.OrderIDs {
		orderIDs = append(orderIDs, id)
	}
	slices.Sort(orderIDs)

	payload := struct {
		Prefix   string   `json:"prefix"`
		Count    int      `json:"count"`
		OrderIDs []string `json:"order_ids"`
	}{
		Prefix:   want,
		Count:    state.TotalCount,
		OrderIDs: orderIDs,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, *url, strings.NewReader(string(body)))
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		log.Fatalf("server returned %s", resp.Status)
	}

	fmt.Printf("Mode=%s | New IDs this run: %d | Cumulative: %d | LastReadAt=%s | LastOffset=%d\n",
		mode, newIDs, state.TotalCount, state.LastReadAt, state.LastOffset)
}
