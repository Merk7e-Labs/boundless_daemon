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
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var (
	completedOrderMarker = "Completed order:"
	timestampRegex       = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z`)
)

type RunResult struct {
	OrdersCompleted int
	WindowStart     time.Time
	WindowEnd       time.Time
	TotalCycles     float64
}

func runOnce(cfg Config, state State) (RunResult, State, error) {
	now := time.Now().UTC()
	since := now.Add(-cfg.InitialLookback)
	if ts, ok := state.Timestamp(); ok {
		since = ts
	}

	renderedCommand := strings.ReplaceAll(cfg.LogCommand, "{service}", cfg.Service)
	renderedCommand = strings.ReplaceAll(renderedCommand, "{since}", since.UTC().Format(time.RFC3339Nano))

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

	cmd := exec.CommandContext(ctx, "sh", "-c", renderedCommand)
	cmd.Dir = cfg.Workdir
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return RunResult{}, state, fmt.Errorf("log command timed out: %w", ctx.Err())
	}
	if err != nil {
		return RunResult{}, state, fmt.Errorf("log command failed: %w (output: %s)", err, bytes.TrimSpace(output))
	}

	result, latest := parseLogs(bytes.NewReader(output), since)
	if latest.After(result.WindowEnd) {
		result.WindowEnd = latest
	}
	result.WindowStart = since
	if result.WindowEnd.Before(result.WindowStart) {
		result.WindowEnd = result.WindowStart
	}

	result.TotalCycles = float64(result.OrdersCompleted) * 0.01

	if !latest.IsZero() && latest.After(since) {
		state.LastTimestamp = formatTimestamp(latest)
	} else if state.LastTimestamp == "" {
		state.LastTimestamp = formatTimestamp(since)
	}

	log.Printf("scraped data: prover=%q orders=%d cycles=%.2f window=[%s -> %s]", cfg.ProverID, result.OrdersCompleted, result.TotalCycles, result.WindowStart.Format(time.RFC3339Nano), result.WindowEnd.Format(time.RFC3339Nano))

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

func parseLogs(r io.Reader, since time.Time) (RunResult, time.Time) {
	var result RunResult
	var latest time.Time

	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		ts, ok := extractTimestamp(line)
		if !ok {
			continue
		}
		if !ts.After(since) {
			continue
		}
		if ts.After(latest) {
			latest = ts
		}
		if strings.Contains(line, completedOrderMarker) {
			result.OrdersCompleted++
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("error scanning logs: %v", err)
	}

	result.WindowEnd = latest
	return result, latest
}

func extractTimestamp(line string) (time.Time, bool) {
	match := timestampRegex.FindString(line)
	if match == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339Nano, match); err == nil {
		return ts.UTC(), true
	}
	if ts, err := time.Parse(time.RFC3339, match); err == nil {
		return ts.UTC(), true
	}
	return time.Time{}, false
}

func postMetrics(cfg Config, result RunResult) error {
	payload := map[string]any{
		"orders_completed":       result.OrdersCompleted,
		"total_cycles_trillions": result.TotalCycles,
		"window_start":           result.WindowStart.UTC().Format(time.RFC3339Nano),
		"window_end":             result.WindowEnd.UTC().Format(time.RFC3339Nano),
		"service":                cfg.Service,
		"prover_id":              cfg.ProverID,
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
