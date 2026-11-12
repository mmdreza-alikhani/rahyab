package dispatch

import (
	"testing"
	"time"

	"rahyab/internal/geo"
)

func newRig(t *testing.T) (*Engine, *Store, *Bus, geo.Projector) {
	t.Helper()
	proj := geo.NewProjector(geo.Point{Lat: CenterLat, Lon: CenterLon})
	store := NewStore(proj, 300)
	store.Reset([]Driver{
		{
			ID: "drv_near", Name: "سعید کریمی", Car: "سمند LX سفید", Plate: "45 ب 289",
			Rating: 4.92, Acceptance: 0.95, Pos: geo.Point{Lat: CenterLat + 0.0035, Lon: CenterLon},
			Heading: 180, State: StateAvailable, IdleSince: time.Now().Add(-2 * time.Minute),
		},
		{
			ID: "drv_far", Name: "مجید رضایی", Car: "پژو ۲۰۶ نقره‌ای", Plate: "22 ج 471",
			Rating: 4.70, Acceptance: 0.80, Pos: geo.Point{Lat: CenterLat - 0.045, Lon: CenterLon},
			Heading: 0, State: StateAvailable, IdleSince: time.Now(),
		},
	})

	bus := NewBus()
	cfg := DefaultConfig()
	cfg.BatchMs = 40
	cfg.OfferTTLSec = 5
	cfg.MaxAttempts = 2
	engine := NewEngine(cfg, store, geo.DefaultRoadModel(), bus, proj)
	engine.Start()
	t.Cleanup(engine.Stop)
	return engine, store, bus, proj
}

func waitOffer(t *testing.T, ch <-chan Event) *Offer {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("bus closed")
			}
			if ev.Type == "offer.created" {
				off, ok := ev.Data.(*Offer)
				if !ok {
					t.Fatalf("unexpected offer payload %T", ev.Data)
				}
				return off
			}
		case <-deadline:
			t.Fatal("timed out waiting for offer.created")
		}
	}
}

func TestEnginePicksNearestThenCommitsTrip(t *testing.T) {
	engine, store, bus, _ := newRig(t)
	ch, cancel := bus.Subscribe()
	defer cancel()

	pickup := geo.Point{Lat: CenterLat, Lon: CenterLon}
	dropoff := geo.Point{Lat: CenterLat - 0.02, Lon: CenterLon + 0.02}

	id, err := engine.Submit(pickup, dropoff)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if id == "" {
		t.Fatal("empty request id")
	}

	off := waitOffer(t, ch)
	if off.DriverID != "drv_near" {
		t.Fatalf("expected the near driver, got %s", off.DriverID)
	}
	if off.EtaSec <= 0 {
		t.Fatalf("eta should be positive, got %.1f", off.EtaSec)
	}

	if _, ok := store.Get("drv_near"); !ok {
		t.Fatal("driver disappeared")
	}
	drv, _ := store.Get("drv_near")
	if drv.State != StateReserved {
		t.Fatalf("driver should be reserved before acceptance, got %s", drv.State)
	}

	// A second reservation attempt must fail: one car, one offer.
	if _, ok := store.Reserve("drv_near", "off_duplicate"); ok {
		t.Fatal("reserving an already reserved driver must fail")
	}

	trip, err := engine.Accept(off.ID)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if trip.Fare <= 0 {
		t.Fatalf("trip fare should be positive, got %d", trip.Fare)
	}

	drv, _ = store.Get("drv_near")
	if drv.State != StateOnTrip || drv.Trip == nil {
		t.Fatalf("driver should be on trip, got %s", drv.State)
	}

	// Accepting twice must fail.
	if _, err := engine.Accept(off.ID); err == nil {
		t.Fatal("second accept should fail")
	}

	_, finished, ok := store.CompleteTrip("drv_near")
	if !ok {
		t.Fatal("complete trip failed")
	}
	if finished.ID != trip.ID {
		t.Fatalf("completed wrong trip: %s vs %s", finished.ID, trip.ID)
	}

	drv, _ = store.Get("drv_near")
	if drv.State != StateAvailable {
		t.Fatalf("driver should be available after trip, got %s", drv.State)
	}
}

func TestDeclineReturnsRequestToQueue(t *testing.T) {
	engine, _, bus, _ := newRig(t)
	ch, cancel := bus.Subscribe()
	defer cancel()

	_, err := engine.Submit(
		geo.Point{Lat: CenterLat, Lon: CenterLon},
		geo.Point{Lat: CenterLat - 0.02, Lon: CenterLon},
	)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	first := waitOffer(t, ch)
	if err := engine.Decline(first.ID); err != nil {
		t.Fatalf("decline: %v", err)
	}

	// The request is retried, so a second offer must arrive.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == "offer.created" {
				off := ev.Data.(*Offer)
				if off.ID == first.ID {
					t.Fatal("duplicate offer id")
				}
				return
			}
		case <-deadline:
			t.Fatal("request was not retried after decline")
		}
	}
}

func TestReleaseRejectsStaleOffer(t *testing.T) {
	proj := geo.NewProjector(geo.Point{Lat: CenterLat, Lon: CenterLon})
	store := NewStore(proj, 300)
	store.Reset([]Driver{{
		ID: "d1", Rating: 5, Acceptance: 1, Pos: geo.Point{Lat: CenterLat, Lon: CenterLon},
		State: StateAvailable, IdleSince: time.Now(),
	}})

	if _, ok := store.Reserve("d1", "off_a"); !ok {
		t.Fatal("reserve failed")
	}
	if store.Release("d1", "off_b") {
		t.Fatal("a stale offer must not release the driver")
	}
	if !store.Release("d1", "off_a") {
		t.Fatal("the owning offer should release the driver")
	}
	if store.Release("d1", "off_a") {
		t.Fatal("double release must be a no-op")
	}
}

func TestFareAndTrafficModel(t *testing.T) {
	a := geo.Point{Lat: CenterLat, Lon: CenterLon}
	b := geo.Point{Lat: CenterLat + 0.05, Lon: CenterLon + 0.05}

	morning := time.Date(2026, 11, 12, 8, 30, 0, 0, time.UTC)
	night := time.Date(2026, 11, 12, 2, 0, 0, 0, time.UTC)
	router := geo.DefaultRoadModel()

	etaMorning := router.ETA(a, b, morning)
	etaNight := router.ETA(a, b, night)
	if etaMorning <= etaNight {
		t.Fatalf("morning traffic should be slower: %.0f vs %.0f", etaMorning, etaNight)
	}

	fare := fareFor(5000, 900)
	if fare < 60000 || fare > 120000 {
		t.Fatalf("unexpected fare for 5 km: %d", fare)
	}
}
