// Package simulator feeds the demo CGM sensors in staging (SRS UC082).
//
// Real CGM sensors cannot be read directly and the Dexcom API is 3 hours late
// outside the US, so the live demo replays traces generated offline with
// simglucose (tools/cgm-traces). It runs inside the API when
// SIMULATOR_ENABLED=true, never in production.
//
// Every CGM sensor in use whose brand_model is "Voltik CGM simulator" gets
// one reading per 5-minute slot of the wall clock, for the last 30 days:
// the first run fills the history and the next ones add the new slot. The
// value of a slot depends only on the sensor and the slot, and the reading's
// idempotency_key is "sim:<sensor>:<slot>", so restarts and several API
// instances never store a reading twice. Readings older than 2 hours raise
// no alerts, so only the live ones do.
package simulator

import (
	"context"
	"embed"
	"encoding/csv"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

//go:embed traces/*.csv
var tracesFS embed.FS

const (
	slot     = 5 * time.Minute
	history  = 30 * 24 * time.Hour
	maxBatch = domain.MaxBatchItems
)

// Trace is a loop of glucose values, one per 5 minutes.
type Trace struct {
	Name   string
	Values []int
}

// LoadTraces reads the embedded traces.
func LoadTraces() ([]Trace, error) {
	names, err := fs.Glob(tracesFS, "traces/*.csv")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var traces []Trace
	for _, name := range names {
		f, err := tracesFS.Open(name)
		if err != nil {
			return nil, err
		}
		rows, err := csv.NewReader(f).ReadAll()
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		t := Trace{Name: name}
		for _, row := range rows[1:] { // header: minute, glucose_mg_dl
			v, err := strconv.Atoi(row[1])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			t.Values = append(t.Values, v)
		}
		if len(t.Values) == 0 {
			return nil, fmt.Errorf("%s: empty trace", name)
		}
		traces = append(traces, t)
	}
	if len(traces) == 0 {
		return nil, errors.New("simulator: no traces")
	}
	return traces, nil
}

// Store is what the simulator needs from the database.
type Store interface {
	SimulatorDevices(ctx context.Context) ([]domain.Device, error)
	ExistingSlots(ctx context.Context, deviceID uuid.UUID, from, to time.Time) (map[int64]bool, error)
}

// Ingester stores readings like any other source, raising alerts for the live ones.
type Ingester interface {
	IngestSimulated(ctx context.Context, patientID uuid.UUID, in []domain.ReadingInput) error
}

// Simulator writes the readings of the demo sensors.
type Simulator struct {
	store  Store
	ingest Ingester
	traces []Trace
	log    *slog.Logger
	now    func() time.Time
}

func New(store Store, ingest Ingester, traces []Trace, log *slog.Logger) *Simulator {
	return &Simulator{store: store, ingest: ingest, traces: traces, log: log, now: time.Now}
}

// Tick fills every slot of the last 30 days that a demo sensor is missing.
func (s *Simulator) Tick(ctx context.Context) {
	devices, err := s.store.SimulatorDevices(ctx)
	if err != nil {
		s.log.Warn("simulator: could not read the demo sensors", "error", err)
		return
	}
	for _, d := range devices {
		if err := s.fill(ctx, d); err != nil {
			s.log.Warn("simulator: could not write readings", "error", err)
		}
	}
}

func (s *Simulator) fill(ctx context.Context, d domain.Device) error {
	now := s.now()
	last := now.Unix() / int64(slot.Seconds())
	first := now.Add(-history).Unix() / int64(slot.Seconds())
	if d.SensorExpiresAt != nil && d.SensorExpiresAt.Before(now) {
		return nil // an expired sensor sends nothing (UC078 A1)
	}
	existing, err := s.store.ExistingSlots(ctx, d.ID, time.Unix(first*int64(slot.Seconds()), 0), now.Add(time.Second))
	if err != nil {
		return err
	}
	trace, offset := s.pick(d.ID)
	var batch []domain.ReadingInput
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := s.ingest.IngestSimulated(ctx, d.PatientID, batch)
		batch = batch[:0]
		return err
	}
	for sl := first; sl <= last; sl++ {
		if existing[sl] {
			continue
		}
		id := d.ID
		value, trend := Reading(trace, sl+offset)
		batch = append(batch, domain.ReadingInput{
			IdempotencyKey: fmt.Sprintf("sim:%s:%d", d.ID, sl),
			MeasuredAt:     time.Unix(sl*int64(slot.Seconds()), 0).UTC(),
			ValueMgDl:      value, Trend: trend, Source: domain.SourceCGM, DeviceID: &id,
		})
		if len(batch) == maxBatch {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

// pick chooses the trace and the starting point of a sensor, so two demo
// patients never show the same curve at the same time.
func (s *Simulator) pick(id uuid.UUID) (Trace, int64) {
	h := fnv.New64a()
	h.Write(id[:])
	sum := h.Sum64()
	trace := s.traces[sum%uint64(len(s.traces))]
	return trace, int64((sum >> 16) % uint64(len(trace.Values)))
}

// Reading is the value of a slot of the trace and its trend, worked out from
// the change since the slot before (in mg/dL per minute).
func Reading(t Trace, slot int64) (int, string) {
	n := int64(len(t.Values))
	i := ((slot % n) + n) % n
	prev := t.Values[(i-1+n)%n]
	v := t.Values[i]
	rate := float64(v-prev) / 5
	trend := domain.TrendStable
	switch {
	case rate > 2:
		trend = domain.TrendRisingFast
	case rate > 1:
		trend = domain.TrendRising
	case rate < -2:
		trend = domain.TrendFallingFast
	case rate < -1:
		trend = domain.TrendFalling
	}
	return v, trend
}
