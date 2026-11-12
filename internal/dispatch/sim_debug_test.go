package dispatch

import (
	"math"
	"testing"

	"rahyab/internal/geo"
)

// TestSimulatorTwoLegTrip guards the pickup -> drop-off state machine. An
// earlier implementation decided the leg from "distance to pickup > 45 m",
// which made the car drive one step toward the destination and then turn back,
// forever. A phase flag fixes that; this test fails if the oscillation returns.
func TestSimulatorTwoLegTrip(t *testing.T) {
	proj := geo.NewProjector(geo.Point{Lat: CenterLat, Lon: CenterLon})
	store := NewStore(proj, 450)

	pickup := geo.Point{Lat: CenterLat, Lon: CenterLon}
	dropoff := geo.Point{Lat: CenterLat - 0.02, Lon: CenterLon + 0.02}
	start := geo.Destination(pickup, 45, 117)

	store.Reset([]Driver{{
		ID:      "d1",
		Pos:     start,
		Heading: 45,
		State:   StateOnTrip,
		Trip: &Trip{
			ID: "t1", DriverID: "d1",
			Pickup: pickup, Dropoff: dropoff,
			Phase: PhaseToPickup,
		},
	}})

	sim := NewSimulator(store, NewBus(), nil)

	pickedUp := false
	previousRemaining := math.Inf(1)
	finished := false

	for tick := 0; tick < 80 && !finished; tick++ {
		sim.tick(0.8)
		d, ok := store.Get("d1")
		if !ok {
			t.Fatal("driver missing")
		}
		if d.Trip != nil && d.Trip.Phase == PhaseToDropoff {
			pickedUp = true
		}
		if pickedUp && d.State == StateOnTrip {
			remaining := geo.DistanceMeters(d.Pos, dropoff)
			if remaining > previousRemaining+1 {
				t.Fatalf("car moved away from the destination on tick %d: %.1f -> %.1f", tick, previousRemaining, remaining)
			}
			previousRemaining = remaining
		}
		if d.State == StateAvailable {
			finished = true
		}
	}

	if !pickedUp {
		t.Fatal("trip never reached the pickup")
	}
	if !finished {
		t.Fatal("trip never completed")
	}
}
