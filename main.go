package main

import (
	"flag"
	"log"
	"time"
)

func main() {
	envPath := flag.String("env", ".env", "path to .env file")
	runOnceFlag := flag.Bool("once", false, "run a single scrape and exit")
	flag.Parse()

	if err := loadEnvFile(*envPath); err != nil {
		log.Fatalf("failed to load env file: %v", err)
	}

	cfg, err := loadConfig()
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
			log.Printf("orders=%d cycles=%.2f window=[%s -> %s]", result.OrdersCompleted, result.TotalCycles, result.WindowStart.Format(time.RFC3339Nano), result.WindowEnd.Format(time.RFC3339Nano))
		}

		if *runOnceFlag {
			break
		}

		time.Sleep(cfg.Interval)
	}
}
