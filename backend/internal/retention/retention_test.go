package retention

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeStore struct {
	locked     bool
	expired    []uuid.UUID // accounts past the retention period, oldest first
	failing    map[uuid.UUID]bool
	erased     []uuid.UUID
	clinicians int
	cutoffs    []time.Time
	unlocked   bool
}

func (f *fakeStore) TryRetentionLock(context.Context) (func(), bool, error) {
	if f.locked {
		return nil, false, nil
	}
	return func() { f.unlocked = true }, true, nil
}

func (f *fakeStore) ExpiredAccounts(_ context.Context, cutoff time.Time, limit int, skip []uuid.UUID) ([]uuid.UUID, error) {
	f.cutoffs = append(f.cutoffs, cutoff)
	var out []uuid.UUID
	for _, id := range f.expired {
		if !slices.Contains(skip, id) && len(out) < limit {
			out = append(out, id)
		}
	}
	return out, nil
}

func (f *fakeStore) EraseAccount(_ context.Context, id uuid.UUID, _ time.Time) error {
	if f.failing[id] {
		return errors.New("still referenced")
	}
	f.erased = append(f.erased, id)
	f.expired = slices.DeleteFunc(f.expired, func(e uuid.UUID) bool { return e == id })
	return nil
}

func (f *fakeStore) CountRetainedClinicians(context.Context, time.Time) (int, error) {
	return f.clinicians, nil
}

func ids(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestRunErasesEveryExpiredAccountInBatches(t *testing.T) {
	store := &fakeStore{expired: ids(2*BatchSize + 5), clinicians: 1}
	now := time.Date(2026, 12, 3, 3, 0, 0, 0, time.UTC)

	res, err := NewJob(store, quiet).Run(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Erased != 2*BatchSize+5 || res.Failed != 0 || res.RetainedClinicians != 1 || len(store.expired) != 0 {
		t.Fatalf("result = %+v, left = %d", res, len(store.expired))
	}
	if !store.cutoffs[0].Equal(now.Add(-30 * 24 * time.Hour)) {
		t.Fatalf("cutoff = %v", store.cutoffs[0])
	}
	if !store.unlocked {
		t.Fatal("lock not released")
	}
}

func TestRunCarriesOnAfterAFailure(t *testing.T) {
	accounts := ids(BatchSize + 1)
	failing := map[uuid.UUID]bool{}
	for _, id := range accounts[:BatchSize] { // a whole batch fails
		failing[id] = true
	}
	store := &fakeStore{expired: slices.Clone(accounts), failing: failing}

	res, err := NewJob(store, quiet).Run(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != BatchSize || res.Erased != 1 || store.erased[0] != accounts[BatchSize] {
		t.Fatalf("result = %+v", res)
	}
}

func TestRunSkipsWhenAnotherInstanceRunsIt(t *testing.T) {
	store := &fakeStore{locked: true, expired: ids(3)}
	res, err := NewJob(store, quiet).Run(context.Background(), time.Now())
	if err != nil || !res.Skipped || len(store.erased) != 0 {
		t.Fatalf("result = %+v, err = %v", res, err)
	}
}

func TestNextRun(t *testing.T) {
	day := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, time.UTC) }
	tests := []struct{ now, want time.Time }{
		{day(2, 1, 0), day(2, 3, 0)},
		{day(2, 3, 0), day(3, 3, 0)},
		{day(2, 23, 59), day(3, 3, 0)},
		{time.Date(2026, 11, 2, 2, 30, 0, 0, time.FixedZone("WEST", 3600)), day(2, 3, 0)}, // 01:30 UTC
	}
	for _, tt := range tests {
		if got := NextRun(tt.now); !got.Equal(tt.want) {
			t.Errorf("NextRun(%v) = %v, want %v", tt.now, got, tt.want)
		}
	}
}
