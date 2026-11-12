package dispatch

import (
	"testing"

	"rahyab/internal/geo"
)

func TestMetricsResetKeepsMutexUsable(t *testing.T) {
	m := &metrics{}
	m.incRequest()
	m.incMatched()
	m.observeSearch(12.5, 30, 8, 90)
	m.observeAssign(0.4)

	if s := m.snapshot(); s.Requests != 1 || s.Matched != 1 {
		t.Fatalf("unexpected counters before reset: %+v", s)
	}

	m.reset()

	s := m.snapshot()
	if s.Requests != 0 || s.Matched != 0 || s.Offers != 0 {
		t.Fatalf("counters not cleared: %+v", s)
	}
	if s.AvgSearchMs != 0 || s.AvgAssignMs != 0 {
		t.Fatalf("latency samples not cleared: %+v", s)
	}

	// The mutex must still be usable after a reset; a previous implementation
	// replaced the whole struct and made the next lock fatal.
	m.incRequest()
	m.observeSearch(3, 10, 4, 60)
	if s := m.snapshot(); s.Requests != 1 || s.AvgSearchUs <= 0 {
		t.Fatalf("metrics unusable after reset: %+v", s)
	}
}

func TestEngineResetClearsRuntimeState(t *testing.T) {
	engine, store, bus, _ := newRig(t)
	ch, cancel := bus.Subscribe()
	defer cancel()

	if _, err := engine.Submit(
		geo.Point{Lat: CenterLat, Lon: CenterLon},
		geo.Point{Lat: CenterLat - 0.02, Lon: CenterLon + 0.01},
	); err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitOffer(t, ch)
	if _, ok := store.Get("drv_near"); !ok {
		t.Fatal("driver missing")
	}

	engine.Reset(SeedDrivers(24))

	drv, ok := store.Get("drv_001")
	if !ok || drv.State != StateAvailable {
		t.Fatalf("seeded fleet not installed: %+v ok=%v", drv, ok)
	}
	if stats := engine.Stats(); stats.Requests != 0 || stats.Accepted != 0 {
		t.Fatalf("stats not reset: %+v", stats)
	}

	// A fresh request must still work after the reset.
	if _, err := engine.Submit(
		geo.Point{Lat: CenterLat, Lon: CenterLon},
		geo.Point{Lat: CenterLat - 0.03, Lon: CenterLon},
	); err != nil {
		t.Fatalf("submit after reset: %v", err)
	}
	waitOffer(t, ch)
}
