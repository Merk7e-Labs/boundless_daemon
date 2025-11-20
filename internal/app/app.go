package app

import (
	"context"
	"log"
	"time"

	"boundless_scraper/internal/config"
	"boundless_scraper/internal/scraper"
	"boundless_scraper/internal/state"
)

type Runner struct {
	cfg config.Config
}

func New(cfg config.Config) *Runner {
	return &Runner{cfg: cfg}
}

func (r *Runner) Run(ctx context.Context, runOnce bool) error {
	st, err := state.Load(r.cfg.StateFile)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		result, updatedState, err := scraper.RunOnce(r.cfg, st)
		if err != nil {
			log.Printf("scrape failed: %v", err)
		} else {
			st = updatedState
			if err := state.Save(r.cfg.StateFile, st); err != nil {
				log.Printf("failed to save state: %v", err)
			}
			log.Printf("prover=%q orders=%d cycles=%.2f window=[%s -> %s]",
				r.cfg.ProverID,
				result.OrdersCompleted,
				result.TotalCycles,
				result.WindowStart.Format(time.RFC3339Nano),
				result.WindowEnd.Format(time.RFC3339Nano),
			)
		}

		if runOnce {
			return err
		}

		wait := time.NewTimer(r.cfg.Interval)
		select {
		case <-ctx.Done():
			wait.Stop()
			return ctx.Err()
		case <-wait.C:
		}
	}
}
