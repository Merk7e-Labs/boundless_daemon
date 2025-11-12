package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

/*
Run:
  # Time-based (default) - finds RFC3339/Nano anywhere in the line:
  go run aggregator.go -file=./test.txt -prefix=0x5a1f4d397 -prover=0x6220892679110898abd78847d6f0a639e3408dc7 -url=http://localhost:9090/api/offchain/report -state=./agg_state.json

  # Offset-based (fallback) - tail-like; ignores timestamps:
  go run aggregator.go -file=./test.txt -prefix=0x5a1f4d397 -prover=0x6220892679110898abd78847d6f0a639e3408dc7 -url=http://localhost:9090/api/offchain/report -state=./agg_state.json -mode=offset

Flags:
  -file     : path to log file
  -prefix   : order id prefix to include (e.g., 0x5a1f4d397)
  -prover   : prover address (0x...) the log belongs to
  -url      : Boundless backend /api/offchain/report endpoint
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

type runnerConfig struct {
	Path       string
	Prefix     string
	ProverAddr string
	URL        string
	StatePath  string
	Mode       Mode
}

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

func defaultValue(envKey, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return v
	}
	return fallback
}

func durationValue(envKey, fallback string) time.Duration {
	raw := strings.TrimSpace(defaultValue(envKey, fallback))
	if raw == "" {
		raw = fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return time.Second * 10
	}
	return d
}

func main() {
	path := flag.String("file", defaultValue("SCRAPER_FILE", "./test.txt"), "path to log file")
	prefix := flag.String("prefix", defaultValue("SCRAPER_PREFIX", "0x5a1f4d397"), "order id prefix to include")
	proverAddr := flag.String("prover", defaultValue("BOUNDLESS_PROVER_ADDR", "0x6220892679110898abd78847d6f0a639e3408dc7"), "prover address (0x...) associated with the log")
	url := flag.String("url", defaultValue("BOUNDLESS_REPORT_URL", "http://localhost:9090/api/offchain/report"), "Boundless backend report endpoint")
	statePath := flag.String("state", defaultValue("SCRAPER_STATE_FILE", "./agg_state.json"), "path to persistent state file")
	modeStr := flag.String("mode", "time", "incremental mode: time|offset")
	intervalStr := flag.String("interval", defaultValue("SCRAPER_INTERVAL", "15m"), "scrape interval (duration, e.g. 15m)")
	logBroker := flag.String("log-broker", defaultValue("SCRAPER_BROKER", "broker2"), "docker compose service to stream logs from")
	logEndpoint := flag.String("log-url", defaultValue("BOUNDLESS_LOG_ENDPOINT", "http://localhost:9090/api/logs"), "dashboard backend log endpoint")
	logIntervalStr := flag.String("log-interval", defaultValue("SCRAPER_LOG_INTERVAL", "10s"), "log posting interval")
	reset := flag.Bool("reset", false, "delete existing state and start from scratch")
	flag.Parse()

	mode := Mode(strings.ToLower(strings.TrimSpace(*modeStr)))
	if mode != ModeTime && mode != ModeOffset {
		log.Fatalf("unknown -mode=%q (use time|offset)", *modeStr)
	}

	interval, err := time.ParseDuration(strings.TrimSpace(*intervalStr))
	if err != nil || interval <= 0 {
		log.Fatalf("invalid -interval %q: %v", *intervalStr, err)
	}
	logInterval, err := time.ParseDuration(strings.TrimSpace(*logIntervalStr))
	if err != nil || logInterval <= 0 {
		logInterval = 10 * time.Second
	}

	cfg := runnerConfig{
		Path:       *path,
		Prefix:     *prefix,
		ProverAddr: *proverAddr,
		URL:        *url,
		StatePath:  *statePath,
		Mode:       mode,
	}

	if *reset {
		_ = os.Remove(cfg.StatePath)
	}

	ctx := context.Background()
	if endpoint := strings.TrimSpace(*logEndpoint); endpoint != "" {
		streamer := &logStreamer{
			broker:     strings.TrimSpace(*logBroker),
			endpoint:   endpoint,
			interval:   logInterval,
			httpClient: &http.Client{Timeout: 10 * time.Second},
		}
		go streamer.Run(ctx)
	} else {
		log.Printf("[logs] streamer disabled (log endpoint not configured)")
	}

	log.Printf("Starting boundless scraper loop (interval %s, UTC aligned)", interval)

	for {
		if err := runOnce(cfg); err != nil {
			log.Printf("scrape error: %v", err)
		}
		sleep := durationUntilNextBoundary(interval)
		next := time.Now().UTC().Add(sleep)
		log.Printf("next run scheduled for %s (in %s)", next.Format(time.RFC3339), sleep.Truncate(time.Second))
		time.Sleep(sleep)
	}
}

func durationUntilNextBoundary(interval time.Duration) time.Duration {
	now := time.Now().UTC()
	bucket := now.Truncate(interval)
	if bucket.Before(now) || bucket.Equal(now) {
		bucket = bucket.Add(interval)
	}
	wait := bucket.Sub(now)
	if wait <= 0 {
		return interval
	}
	return wait
}

type logStreamer struct {
	broker     string
	endpoint   string
	interval   time.Duration
	httpClient *http.Client
	lastSince  time.Time
}

func (ls *logStreamer) Run(ctx context.Context) {
	if ls == nil || ls.broker == "" || ls.endpoint == "" || ls.interval <= 0 {
		log.Printf("[logs] streamer disabled (invalid configuration)")
		return
	}
	log.Printf("[logs] streaming docker compose logs for %s every %s", ls.broker, ls.interval)
	ticker := time.NewTicker(ls.interval)
	defer ticker.Stop()
	for {
		if err := ls.collectAndSend(ctx); err != nil {
			log.Printf("[logs] %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (ls *logStreamer) collectAndSend(ctx context.Context) error {
	since := ls.lastSince
	if since.IsZero() {
		since = time.Now().UTC().Add(-ls.interval)
	}
	lines, err := ls.fetchLogs(ctx, since)
	if err != nil {
		return fmt.Errorf("fetch logs: %w", err)
	}
	ls.lastSince = time.Now().UTC()
	if len(lines) == 0 {
		return nil
	}
	return ls.postLogs(ctx, lines)
}

func (ls *logStreamer) fetchLogs(ctx context.Context, since time.Time) ([]string, error) {
	sinceStr := since.Format(time.RFC3339)
	cmd := exec.CommandContext(ctx, "docker", "compose", "logs", ls.broker, "--since", sinceStr, "--no-color")
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg != "" {
			return nil, fmt.Errorf("docker compose logs: %v: %s", err, msg)
		}
		return nil, err
	}
	raw := strings.Split(string(output), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lines = append(lines, trimmed)
	}
	return lines, nil
}

func (ls *logStreamer) postLogs(ctx context.Context, lines []string) error {
	payload := map[string]any{
		"broker":      ls.broker,
		"lines":       lines,
		"captured_at": time.Now().UTC().Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ls.endpoint, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := ls.httpClient.Do(req)
		if err == nil && resp.StatusCode/100 == 2 {
			resp.Body.Close()
			return nil
		}
		if err == nil {
			lastErr = fmt.Errorf("POST %s: %s", ls.endpoint, resp.Status)
			resp.Body.Close()
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	return lastErr
}

func runOnce(cfg runnerConfig) error {
	state, err := loadState(cfg.StatePath)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	want := strings.ToLower(strings.TrimSpace(cfg.Prefix))
	prover := strings.ToLower(strings.TrimSpace(cfg.ProverAddr))
	if prover == "" {
		return fmt.Errorf("prover address is required")
	}

	f, err := os.Open(cfg.Path)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat log file: %w", err)
	}

	newIDs := 0

	switch cfg.Mode {
	case ModeTime:
		var lastRead time.Time
		if state.LastReadAt != "" {
			if t, parseErr := time.Parse(time.RFC3339, state.LastReadAt); parseErr == nil {
				lastRead = t
			} else {
				log.Printf("warning: invalid LastReadAt in state (%q): %v", state.LastReadAt, parseErr)
			}
		}

		sc := bufio.NewScanner(f)
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 2*1024*1024)

		maxSeen := lastRead
		for sc.Scan() {
			line := sc.Text()

			ts, ok := parseTimestampAnywhere(line)
			if !ok {
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
			return err
		}

		if maxSeen.After(lastRead) {
			state.LastReadAt = maxSeen.Format(time.RFC3339)
		}

	case ModeOffset:
		key := fileKey(fi)
		if state.FileKey != key || state.LastOffset > fi.Size() {
			state.LastOffset = 0
			state.FileKey = key
		}

		if state.LastOffset > 0 {
			if _, err := f.Seek(state.LastOffset, 0); err != nil {
				return fmt.Errorf("seek: %w", err)
			}
		}

		sc := bufio.NewScanner(f)
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 2*1024*1024)

		readBytes := state.LastOffset
		for sc.Scan() {
			line := sc.Text()
			readBytes += int64(len(line)) + 1

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
			return err
		}
		state.LastOffset = readBytes

	default:
		return fmt.Errorf("unsupported mode %q", cfg.Mode)
	}

	state.TotalCount = len(state.OrderIDs)

	if err := saveStateAtomic(cfg.StatePath, state); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	orderIDs := make([]string, 0, len(state.OrderIDs))
	for id := range state.OrderIDs {
		orderIDs = append(orderIDs, id)
	}
	slices.Sort(orderIDs)

	capturedAt := state.LastReadAt
	if strings.TrimSpace(capturedAt) == "" {
		capturedAt = time.Now().UTC().Format(time.RFC3339)
	}

	payload := struct {
		ProverAddr  string   `json:"prover_addr"`
		OrderPrefix string   `json:"order_prefix"`
		Count       int      `json:"count"`
		OrderIDs    []string `json:"order_ids"`
		CapturedAt  string   `json:"captured_at"`
	}{
		ProverAddr:  prover,
		OrderPrefix: want,
		Count:       state.TotalCount,
		OrderIDs:    orderIDs,
		CapturedAt:  capturedAt,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, cfg.URL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("server returned %s", resp.Status)
	}

	log.Printf("Mode=%s | New IDs this run: %d | Cumulative: %d | LastReadAt=%s | LastOffset=%d",
		cfg.Mode, newIDs, state.TotalCount, state.LastReadAt, state.LastOffset)

	return nil
}
