package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"boundless_scraper/internal/filesystem"
)

type Config struct {
	Endpoint        string
	Service         string
	LogFile         string
	LogCommand      string
	Workdir         string
	EnvFile         string
	BrokerEnvFile   string
	ProverID        string
	ProverAddress   string
	Interval        time.Duration
	InitialLookback time.Duration
	StateFile       string
	CommandTimeout  time.Duration
	PostTimeout     time.Duration
}

func Load(defaultEnvFile, defaultBrokerEnvFile string) (Config, error) {
	cfg := Config{
		Endpoint:        strings.TrimSpace(os.Getenv("SCRAPER_ENDPOINT")),
		Service:         firstNonEmpty(os.Getenv("SCRAPER_SERVICE"), "broker"),
		LogFile:         strings.TrimSpace(os.Getenv("SCRAPER_LOG_FILE")),
		LogCommand:      firstNonEmpty(os.Getenv("SCRAPER_LOG_COMMAND"), "docker compose logs {service} --since {since} --no-color"),
		Workdir:         firstNonEmpty(os.Getenv("SCRAPER_WORKDIR"), "~/boundless"),
		EnvFile:         firstNonEmpty(os.Getenv("SCRAPER_ENV_FILE"), defaultEnvFile),
		BrokerEnvFile:   firstNonEmpty(os.Getenv("SCRAPER_BROKER_ENV_FILE"), defaultBrokerEnvFile),
		StateFile:       firstNonEmpty(os.Getenv("SCRAPER_STATE_FILE"), "scraper_state.json"),
		ProverID:        firstNonEmpty(os.Getenv("SCRAPER_PROVER_ID"), os.Getenv("PROVER_ID")),
		ProverAddress:   firstNonEmpty(os.Getenv("SCRAPER_PROVER_ADDRESS"), os.Getenv("PROVER_ADDRESS")),
		Interval:        parseDurationEnv("SCRAPER_INTERVAL", 15*time.Minute),
		InitialLookback: parseDurationEnv("SCRAPER_INITIAL_LOOKBACK", 10*time.Minute),
		CommandTimeout:  parseDurationEnv("SCRAPER_COMMAND_TIMEOUT", time.Minute),
		PostTimeout:     parseDurationEnv("SCRAPER_POST_TIMEOUT", 15*time.Second),
	}

	if cfg.Endpoint == "" {
		return Config{}, fmt.Errorf("SCRAPER_ENDPOINT must be set")
	}

	if cfg.LogFile != "" {
		logPath, err := filesystem.ExpandPath(cfg.LogFile)
		if err != nil {
			return Config{}, err
		}
		cfg.LogFile = logPath
	}

	workdir, err := filesystem.ExpandPath(cfg.Workdir)
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

	statePath, err := filesystem.ExpandPath(cfg.StateFile)
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

	expanded, err := filesystem.ExpandPath(path)
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
