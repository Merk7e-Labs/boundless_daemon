package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Endpoint        string
	Service         string
	LogCommand      string
	Workdir         string
	EnvFile         string
	BrokerEnvFile   string
	ProverID        string
	Interval        time.Duration
	InitialLookback time.Duration
	StateFile       string
	CommandTimeout  time.Duration
	PostTimeout     time.Duration
}

func loadEnvFile(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			continue
		}
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, "\"'")
		_ = os.Setenv(key, value)
	}
	return scanner.Err()
}

func loadConfig(defaultEnvFile, defaultBrokerEnvFile string) (Config, error) {
	cfg := Config{
		Endpoint:        strings.TrimSpace(os.Getenv("SCRAPER_ENDPOINT")),
		Service:         firstNonEmpty(os.Getenv("SCRAPER_SERVICE"), "broker"),
		LogCommand:      firstNonEmpty(os.Getenv("SCRAPER_LOG_COMMAND"), "docker compose logs {service} --since {since} --no-color"),
		Workdir:         firstNonEmpty(os.Getenv("SCRAPER_WORKDIR"), "~/boundless"),
		EnvFile:         firstNonEmpty(os.Getenv("SCRAPER_ENV_FILE"), defaultEnvFile),
		BrokerEnvFile:   firstNonEmpty(os.Getenv("SCRAPER_BROKER_ENV_FILE"), defaultBrokerEnvFile),
		StateFile:       firstNonEmpty(os.Getenv("SCRAPER_STATE_FILE"), "scraper_state.json"),
		ProverID:        firstNonEmpty(os.Getenv("SCRAPER_PROVER_ID"), os.Getenv("PROVER_ID")),
		Interval:        parseDurationEnv("SCRAPER_INTERVAL", time.Minute),
		InitialLookback: parseDurationEnv("SCRAPER_INITIAL_LOOKBACK", 10*time.Minute),
		CommandTimeout:  parseDurationEnv("SCRAPER_COMMAND_TIMEOUT", time.Minute),
		PostTimeout:     parseDurationEnv("SCRAPER_POST_TIMEOUT", 15*time.Second),
	}

	if cfg.Endpoint == "" {
		return Config{}, fmt.Errorf("SCRAPER_ENDPOINT must be set")
	}

	workdir, err := expandPath(cfg.Workdir)
	if err != nil {
		return Config{}, err
	}
	cfg.Workdir = workdir

	cfg.EnvFile, err = resolveOptionalFile(cfg.EnvFile)
	if err != nil {
		return Config{}, err
	}

	cfg.BrokerEnvFile, err = resolveOptionalFile(cfg.BrokerEnvFile)
	if err != nil {
		return Config{}, err
	}

	statePath, err := expandPath(cfg.StateFile)
	if err != nil {
		return Config{}, err
	}
	cfg.StateFile = statePath

	if cfg.Interval <= 0 {
		return Config{}, fmt.Errorf("interval must be positive")
	}
	if cfg.CommandTimeout <= 0 {
		return Config{}, fmt.Errorf("command timeout must be positive")
	}
	if cfg.PostTimeout <= 0 {
		return Config{}, fmt.Errorf("post timeout must be positive")
	}

	return cfg, nil
}

func resolveOptionalFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}

	expanded, err := expandPath(path)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(expanded); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}

	return expanded, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}

func expandPath(path string) (string, error) {
	if path == "" {
		return path, nil
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	return filepath.Abs(path)
}
