package dispatch

import (
	"testing"
	"time"

	"rahyab/internal/geo"
)

// TestSearchThroughput pushes 5000 lookups through the grid and asserts the
// per-query cost stays in the tens of microseconds. It doubles as a smoke test
// that repeated reads do not corrupt the index.
func TestSearchThroughput(t *testing.T) {
	proj := geo.NewProjector(geo.Point{Lat: CenterLat, Lon: CenterLon})
	store := NewStore(proj, 450)
	store.Reset(SeedDrivers(72))
	pickup := geo.Point{Lat: CenterLat, Lon: CenterLon}

	const queries = 5000
	start := time.Now()
	for i := 0; i < queries; i++ {
		if _, st := store.SearchAvailable(pickup, 7000, 18, nil, nil); st.Scanned == 0 {
			t.Fatal("query scanned no drivers")
		}
	}
	perQuery := time.Since(start) / queries
	t.Logf("throughput: %v per query", perQuery)
	if perQuery > 500*time.Microsecond {
		t.Fatalf("index is slower than the 500 µs budget: %v", perQuery)
	}
}
