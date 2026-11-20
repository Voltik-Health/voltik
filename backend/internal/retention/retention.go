// Package retention erases, every day at 03:00 UTC, the accounts deleted more
// than 30 days ago (GDPR right to erasure, SRS UC018, ADR 0006).
package retention

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

// BatchSize is how many accounts are read at a time.
const BatchSize = 100

// RunAt is the time of day, in UTC, when the job runs.
const RunAt = 3 * time.Hour

type Store interface {
	TryRetentionLock(ctx context.Context) (unlock func(), ok bool, err error)
	ExpiredAccounts(ctx context.Context, cutoff time.Time, limit int, skip []uuid.UUID) ([]uuid.UUID, error)
	EraseAccount(ctx context.Context, userID uuid.UUID, cutoff time.Time) error
	CountRetainedClinicians(ctx context.Context, cutoff time.Time) (int, error)
}

// Result counts what one run did. It never holds ids.
type Result struct {
	Erased             int
	Failed             int
	RetainedClinicians int  // deleted clinicians kept: their follow-ups reference them
	Skipped            bool // another API instance was running the job
}

type Job struct {
	store Store
	log   *slog.Logger
}

func NewJob(store Store, log *slog.Logger) *Job {
	return &Job{store: store, log: log}
}

// Run erases every account deleted before now minus the retention period.
// Each account is erased in its own transaction: one that fails is logged and
// left for the next run, and the job carries on with the others.
func (j *Job) Run(ctx context.Context, now time.Time) (Result, error) {
	var res Result
	unlock, ok, err := j.store.TryRetentionLock(ctx)
	if err != nil {
		return res, err
	}
	if !ok {
		res.Skipped = true
		return res, nil
	}
	defer unlock()

	cutoff := now.Add(-domain.RetentionPeriod)
	var failed []uuid.UUID
	for {
		ids, err := j.store.ExpiredAccounts(ctx, cutoff, BatchSize, failed)
		if err != nil {
			return res, err
		}
		for _, id := range ids {
			if err := j.store.EraseAccount(ctx, id, cutoff); err != nil {
				if ctx.Err() != nil {
					return res, ctx.Err()
				}
				j.log.Error("retention: could not erase an account", "error", err)
				failed = append(failed, id)
				res.Failed++
				continue
			}
			res.Erased++
		}
		if len(ids) < BatchSize {
			break
		}
	}
	res.RetainedClinicians, err = j.store.CountRetainedClinicians(ctx, cutoff)
	return res, err
}

// Schedule runs the job every day at 03:00 UTC until ctx is cancelled.
func (j *Job) Schedule(ctx context.Context) {
	for {
		wait := time.Until(NextRun(time.Now()))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		res, err := j.Run(ctx, time.Now())
		switch {
		case err != nil:
			j.log.Error("retention job failed", "error", err, "erased", res.Erased, "failed", res.Failed)
		case res.Skipped:
			j.log.Info("retention job skipped: another instance is running it")
		default:
			j.log.Info("retention job finished",
				"erased", res.Erased, "failed", res.Failed, "retained_clinicians", res.RetainedClinicians)
		}
	}
}

// NextRun is the next 03:00 UTC strictly after now.
func NextRun(now time.Time) time.Time {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Add(RunAt)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
