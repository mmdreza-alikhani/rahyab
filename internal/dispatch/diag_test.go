package dispatch

import (
	"testing"
	"time"

	"rahyab/internal/geo"
)

// TestSearchCandidatesWithinBudget is a coarse performance guard for the
// spatial index. It measures total time over many queries instead of a single
// one: some virtualised Windows hosts expose a monotonic clock whose resolution
// is far coarser than one search (about 14 µs), so a single time.Since reads as
// zero there even though the work clearly happened.
func TestSearchCandidatesWithinBudget(t *testing.T) {
	proj := geo.NewProjector(geo.Point{Lat: CenterLat, Lon: CenterLon})
	store := NewStore(proj, 450)
	store.Reset(SeedDrivers(72))
	pickup := geo.Point{Lat: CenterLat, Lon: CenterLon}

	const queries = 2000
	start := time.Now()
	for i := 0; i < queries; i++ {
		drivers, st := store.SearchAvailable(pickup, 7000, 18, nil, nil)
		if len(drivers) != 18 {
			t.Fatalf("expected 18 candidates, got %d", len(drivers))
		}
		if st.Cells == 0 {
			t.Fatal("index visited no cells")
		}
	}
	perQuery := time.Since(start) / queries
	t.Logf("average indexed search: %v over %d queries", perQuery, queries)
	if perQuery > 2*time.Millisecond {
		t.Fatalf("search too slow: %v per query", perQuery)
	}
}
