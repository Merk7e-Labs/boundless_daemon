package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var (
	// Match timestamps like 2025-11-07T08:57:30.266799Z anywhere in a line
	timestampRegex = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z`)

	// Match only Completed order lines whose order ID starts with 0x5a1f4d
	targetOrderRegex = regexp.MustCompile(`✨\s*Completed order:\s*(0x5a1f4d[a-fA-F0-9]+)`)

	completedOrderPrefix = "0x5a1f4d"
)

// RunResult holds scrape metrics for one run
type RunResult struct {
	OrdersCompleted int
	OrderIDs        []string
	WindowStart     time.Time
	WindowEnd       time.Time
	TotalCycles     float64
}

// runOnce performs one scrape iteration
func runOnce(cfg Config, state State) (RunResult, State, error) {
	now := time.Now().UTC()

	var since time.Time
	var sinceArg string
	var logReader io.Reader

	if ts, ok := state.Timestamp(); ok {
		since = ts
		sinceArg = fmt.Sprintf("--since %s", since.UTC().Format(time.RFC3339Nano))
		log.Printf("Resuming from last timestamp: %s", sinceArg)
	} else {
		log.Println("No previous timestamp found; scraping full log history.")
	}

	if cfg.LogFile != "" {
		log.Printf("reading logs from file %s", cfg.LogFile)
		f, err := os.Open(cfg.LogFile)
		if err != nil {
			return RunResult{}, state, fmt.Errorf("failed to open log file: %w", err)
		}
		defer f.Close()
		logReader = f
	} else {
		// Replace placeholders; support templates where {since} is the whole flag
		renderedCommand := strings.ReplaceAll(cfg.LogCommand, "{service}", cfg.Service)
		if sinceArg == "" {
			if strings.Contains(renderedCommand, "--since {since}") {
				renderedCommand = strings.ReplaceAll(renderedCommand, "--since {since}", "")
			} else {
				renderedCommand = strings.ReplaceAll(renderedCommand, "{since}", "")
			}
		} else {
			renderedCommand = strings.ReplaceAll(renderedCommand, "{since}", sinceArg)
		}

		// Load .env sources with bash
		var envSources []string
		if strings.TrimSpace(cfg.EnvFile) != "" {
			envSources = append(envSources, cfg.EnvFile)
		}
		if strings.TrimSpace(cfg.BrokerEnvFile) != "" {
			envSources = append(envSources, cfg.BrokerEnvFile)
		}
		if len(envSources) > 0 {
			parts := []string{"set -a"}
			for _, src := range envSources {
				parts = append(parts, fmt.Sprintf("source %s", shellQuote(src)))
			}
			parts = append(parts, "set +a", renderedCommand)
			renderedCommand = strings.Join(parts, " && ")
		}

		ctx, cancel := context.WithTimeout(context.Background(), cfg.CommandTimeout)
		defer cancel()

		// Use bash to support "source"
		cmd := exec.CommandContext(ctx, "bash", "-c", renderedCommand)
		cmd.Dir = cfg.Workdir
		output, err := cmd.CombinedOutput()
		if ctx.Err() == context.DeadlineExceeded {
			return RunResult{}, state, fmt.Errorf("log command timed out: %w", ctx.Err())
		}
		if err != nil {
			return RunResult{}, state, fmt.Errorf("log command failed: %w (output: %s)", err, bytes.TrimSpace(output))
		}
		logReader = bytes.NewReader(output)
	}

	result, latest := parseLogs(logReader, since)
	if latest.After(result.WindowEnd) {
		result.WindowEnd = latest
	}
	result.WindowStart = since
	if result.WindowEnd.Before(result.WindowStart) {
		result.WindowEnd = result.WindowStart
	}

	result.TotalCycles = float64(result.OrdersCompleted) * 0.01

	// Save latest timestamp
	if !latest.IsZero() && (state.LastTimestamp == "" || latest.After(since)) {
		state.LastTimestamp = formatTimestamp(latest)
	} else if state.LastTimestamp == "" {
		state.LastTimestamp = formatTimestamp(now)
	}

	log.Printf("scraped data: prover=%q orders=%d (prefix=%s) cycles=%.2f window=[%s -> %s]",
		cfg.ProverID, result.OrdersCompleted, completedOrderPrefix, result.TotalCycles,
		result.WindowStart.Format(time.RFC3339Nano), result.WindowEnd.Format(time.RFC3339Nano))

	if err := postMetrics(cfg, result); err != nil {
		return RunResult{}, state, err
	}

	return result, state, nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// parseLogs scans logs for completed orders with the 0x5a1f4d prefix
func parseLogs(r io.Reader, since time.Time) (RunResult, time.Time) {
	var result RunResult
	var latest time.Time
	var lineCount int

	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 256*1024)
	scanner.Buffer(buf, 2*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		lineCount++

		ts, hasTs := extractTimestamp(line)
		if hasTs && ts.After(latest) {
			latest = ts
		}

		if matches := targetOrderRegex.FindStringSubmatch(line); len(matches) > 1 {
			if hasTs && !ts.After(since) {
				continue
			}
			if !hasTs && !since.IsZero() {
				// Without timestamps we cannot ensure incremental progress, so skip when resuming.
				continue
			}
			result.OrdersCompleted++
			result.OrderIDs = append(result.OrderIDs, matches[1])
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("error scanning logs: %v", err)
	}

	log.Printf("parsed %d lines, found %d orders with prefix %s", lineCount, result.OrdersCompleted, completedOrderPrefix)
	result.WindowEnd = latest
	return result, latest
}

func extractTimestamp(line string) (time.Time, bool) {
	match := timestampRegex.FindString(line)
	if match == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, match)
	if err != nil {
		return time.Time{}, false
	}
	return ts.UTC(), true
}

// postMetrics sends results to the configured endpoint
func postMetrics(cfg Config, result RunResult) error {
	payload := map[string]any{
		"orders_completed":       result.OrdersCompleted,
		"total_cycles_trillions": result.TotalCycles,
		"window_start":           result.WindowStart.UTC().Format(time.RFC3339Nano),
		"window_end":             result.WindowEnd.UTC().Format(time.RFC3339Nano),
		"service":                cfg.Service,
		"prover_id":              cfg.ProverID,
		"prefix":                 completedOrderPrefix,
		"order_ids":              result.OrderIDs,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.PostTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	token := os.Getenv("SCRAPER_AUTH_TOKEN")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("posting metrics timed out: %w", err)
		}
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("metrics endpoint returned status %s", resp.Status)
	}

	return nil
}
