package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"boundless_scraper/internal/app"
	"boundless_scraper/internal/config"
	"boundless_scraper/internal/envfile"
	"boundless_scraper/internal/filesystem"
)

const brokerEnvPath = "~/boundless/.env.broker"

func main() {
	envPath := flag.String("env", ".env.example", "path to .env file")
	runOnceFlag := flag.Bool("once", false, "run a single scrape and exit")
	flag.Parse()

	expandedEnvPath := *envPath
	if expandedEnvPath != "" {
		var err error
		expandedEnvPath, err = filesystem.ExpandPath(expandedEnvPath)
		if err != nil {
			log.Fatalf("failed to resolve env file path: %v", err)
		}
	}

	if err := envfile.Load(expandedEnvPath); err != nil {
		log.Fatalf("failed to load env file: %v", err)
	}

	configEnvPath := expandedEnvPath
	if value := os.Getenv("SCRAPER_ENV_FILE"); value != "" {
		configEnvPath = value
	}
	if configEnvPath != "" {
		var err error
		configEnvPath, err = filesystem.ExpandPath(configEnvPath)
		if err != nil {
			log.Fatalf("failed to resolve config env file path: %v", err)
		}
		if configEnvPath != expandedEnvPath {
			if err := envfile.Load(configEnvPath); err != nil {
				log.Fatalf("failed to load config env file: %v", err)
			}
		}
	}

	brokerEnv := brokerEnvPath
	if value := os.Getenv("SCRAPER_BROKER_ENV_FILE"); value != "" {
		brokerEnv = value
	}
	if brokerEnv != "" {
		var err error
		brokerEnv, err = filesystem.ExpandPath(brokerEnv)
		if err != nil {
			log.Fatalf("failed to resolve broker env file path: %v", err)
		}
	}

	if err := envfile.Load(brokerEnv); err != nil {
		log.Fatalf("failed to load broker env file: %v", err)
	}

	cfg, err := config.Load(configEnvPath, brokerEnv)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	log.Printf("starting scraper for service %q using command %q", cfg.Service, cfg.LogCommand)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runner := app.New(cfg)
	if err := runner.Run(ctx, *runOnceFlag); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("scraper exited with error: %v", err)
	}
}
