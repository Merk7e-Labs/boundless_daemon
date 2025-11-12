package main

import (
	"flag"
	"log"
	"os"
	"time"
)

const brokerEnvPath = "~/boundless/.env.broker"

func main() {
	envPath := flag.String("env", ".env", "path to .env file")
	runOnceFlag := flag.Bool("once", false, "run a single scrape and exit")
	flag.Parse()

	expandedEnvPath := *envPath
	if expandedEnvPath != "" {
		var err error
		expandedEnvPath, err = expandPath(expandedEnvPath)
		if err != nil {
			log.Fatalf("failed to resolve env file path: %v", err)
		}
	}

	if err := loadEnvFile(expandedEnvPath); err != nil {
		log.Fatalf("failed to load env file: %v", err)
	}

	configEnvPath := expandedEnvPath
	if value := os.Getenv("SCRAPER_ENV_FILE"); value != "" {
		configEnvPath = value
	}
	if configEnvPath != "" {
		var err error
		configEnvPath, err = expandPath(configEnvPath)
		if err != nil {
			log.Fatalf("failed to resolve config env file path: %v", err)
		}
		if configEnvPath != expandedEnvPath {
			if err := loadEnvFile(configEnvPath); err != nil {
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
		brokerEnv, err = expandPath(brokerEnv)
		if err != nil {
			log.Fatalf("failed to resolve broker env file path: %v", err)
		}
	}

	if err := loadEnvFile(brokerEnv); err != nil {
		log.Fatalf("failed to load broker env file: %v", err)
	}

	cfg, err := loadConfig(configEnvPath, brokerEnv)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	log.Printf("starting scraper for service %q using command %q", cfg.Service, cfg.LogCommand)

	state, err := loadState(cfg.StateFile)
	if err != nil {
		log.Fatalf("failed to load state: %v", err)
	}

	for {
		result, newState, err := runOnce(cfg, state)
		if err != nil {
			log.Printf("scrape failed: %v", err)
		} else {
			state = newState
			if err := saveState(cfg.StateFile, state); err != nil {
				log.Printf("failed to save state: %v", err)
			}
			log.Printf("prover=%q orders=%d cycles=%.2f window=[%s -> %s]", cfg.ProverID, result.OrdersCompleted, result.TotalCycles, result.WindowStart.Format(time.RFC3339Nano), result.WindowEnd.Format(time.RFC3339Nano))
		}

		if *runOnceFlag {
			break
		}

		time.Sleep(cfg.Interval)
	}
}
