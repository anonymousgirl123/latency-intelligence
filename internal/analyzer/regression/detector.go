package regression

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/kamini/latency-intelligence/internal/config"
	"github.com/kamini/latency-intelligence/internal/store"
)

// Detector compares latency profiles between two commits and
// identifies regressions that exceed the configured threshold.
type Detector struct {
	store *store.ClickHouseStore
	cfg   *config.Config
}

func NewDetector(
	st *store.ClickHouseStore,
	cfg *config.Config,
) *Detector {

	return &Detector{
		store: st,
		cfg:   cfg,
	}
}

// Compare checks if the candidate commit has worse p99 than the baseline commit
// for a given method. Returns a RegressionReport regardless of outcome.
func (d *Detector) Compare(
	ctx context.Context,
	method,
	environment,
	baselineCommit,
	candidateCommit string,
) (*store.RegressionReport, error) {

	baseline, err := d.store.GetStatsByCommit(
		ctx,
		method,
		environment,
		baselineCommit,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"baseline stats for %s@%s: %w",
			method,
			baselineCommit,
			err,
		)
	}

	candidate, err := d.store.GetStatsByCommit(
		ctx,
		method,
		environment,
		candidateCommit,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"candidate stats for %s@%s: %w",
			method,
			candidateCommit,
			err,
		)
	}

	// Require minimum sample count before declaring a regression
	if baseline.SampleCount < int64(d.cfg.MinSampleCount) ||
		candidate.SampleCount < int64(d.cfg.MinSampleCount) {

		log.Printf(
			"[regression] Insufficient samples for %s (baseline=%d, candidate=%d) — skipping",
			method,
			baseline.SampleCount,
			candidate.SampleCount,
		)

		return nil, nil
	}

	deltaMs := candidate.P99Ms - baseline.P99Ms
	deltaPct := deltaMs / baseline.P99Ms

	isRegression := deltaPct > d.cfg.RegressionThresholdPct

	report := &store.RegressionReport{
		Method:          method,
		FilePath:        baseline.FilePath,
		Environment:     environment,
		ServiceName:     baseline.ServiceName,
		BaselineCommit:  baselineCommit,
		CandidateCommit: candidateCommit,
		BaselineP99Ms:   baseline.P99Ms,
		CandidateP99Ms:  candidate.P99Ms,
		DeltaMs:         deltaMs,
		DeltaPct:        deltaPct,
		IsRegression:    isRegression,
		DetectedAt:      time.Now(),
	}

	if isRegression {

		log.Printf(
			"[regression] ⚠️ %s: p99 increased %.0fms -> %.0fms (+%.1f%%) [%s -> %s]",
			method,
			baseline.P99Ms,
			candidate.P99Ms,
			deltaPct*100,
			baselineCommit[:7],
			candidateCommit[:7],
		)

	} else {

		log.Printf(
			"[regression] ✅ %s: p99 %.0fms -> %.0fms (%.1f%%) — OK",
			method,
			baseline.P99Ms,
			candidate.P99Ms,
			deltaPct*100,
		)
	}

	return report, nil
}

// CompareAll checks all methods that have data for both commits.
func (d *Detector) CompareAll(
	ctx context.Context,
	environment,
	baselineCommit,
	candidateCommit string,
	methods []string,
) ([]*store.RegressionReport, error) {

	var reports []*store.RegressionReport

	for _, method := range methods {

		report, err := d.Compare(
			ctx,
			method,
			environment,
			baselineCommit,
			candidateCommit,
		)

		if err != nil {
			log.Printf(
				"[regression] Skipping %s: %v",
				method,
				err,
			)
			continue
		}

		if report != nil {
			reports = append(reports, report)
		}
	}

	return reports, nil
}
