package index

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"
)

type refPoint struct {
	id   string
	x, y float64
}

func bruteNearest(items []refPoint, x, y, maxR float64, k int) []refPoint {
	out := make([]refPoint, 0, len(items))
	for _, it := range items {
		if math.Hypot(it.x-x, it.y-y) <= maxR {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return math.Hypot(out[i].x-x, out[i].y-y) < math.Hypot(out[j].x-x, out[j].y-y)
	})
	if len(out) > k {
		out = out[:k]
	}
	return out
}

// TestNearestKMatchesBruteForce is the safety net for the ring-expansion early
// exit: any mistake in the stopping bound would drop a closer driver, and this
// test compares the result against a full scan on randomised cities.
func TestNearestKMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	g := New(350)

	items := make([]refPoint, 0, 2500)
	for i := 0; i < 2500; i++ {
		p := refPoint{
			id: fmt.Sprintf("d%04d", i),
			x:  rng.Float64()*18000 - 9000,
			y:  rng.Float64()*18000 - 9000,
		}
		items = append(items, p)
		g.Upsert(Item{ID: p.id, X: p.x, Y: p.y})
	}

	for q := 0; q < 250; q++ {
		x := rng.Float64()*20000 - 10000
		y := rng.Float64()*20000 - 10000
		maxR := 500 + rng.Float64()*4500
		k := 1 + rng.Intn(12)

		got, st := g.NearestK(x, y, maxR, k, nil, nil)
		want := bruteNearest(items, x, y, maxR, k)

		if len(got) != len(want) {
			t.Fatalf("query (%.1f,%.1f) k=%d r=%.0f: got %d results, want %d", x, y, k, maxR, len(got), len(want))
		}
		for i := range got {
			gd := math.Hypot(got[i].X-x, got[i].Y-y)
			wd := math.Hypot(want[i].x-x, want[i].y-y)
			if math.Abs(gd-wd) > 1e-6 {
				t.Fatalf("result %d distance mismatch: got %.6f want %.6f (%s vs %s)", i, gd, wd, got[i].ID, want[i].id)
			}
		}
		if st.ResultSet != len(want) {
			t.Fatalf("stats mismatch: %d vs %d", st.ResultSet, len(want))
		}
	}
}

func TestUpsertMovesAndRemove(t *testing.T) {
	g := New(200)
	g.Upsert(Item{ID: "a", X: 0, Y: 0})
	g.Upsert(Item{ID: "b", X: 10000, Y: 10000})

	if got, _ := g.NearestK(0, 0, 500, 5, nil, nil); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("unexpected initial result: %+v", got)
	}

	g.Upsert(Item{ID: "a", X: 9000, Y: 9000})
	if got, _ := g.NearestK(0, 0, 500, 5, nil, nil); len(got) != 0 {
		t.Fatalf("expected empty after move, got %+v", got)
	}
	if got, _ := g.NearestK(9000, 9000, 1600, 5, nil, nil); len(got) != 2 {
		t.Fatalf("expected both items near new position, got %+v", got)
	}

	g.Remove("a")
	if g.Len() != 1 {
		t.Fatalf("expected 1 item after remove, got %d", g.Len())
	}
	g.Remove("missing")
	if g.Len() != 1 {
		t.Fatalf("removing an unknown id must be a no-op")
	}
}

func TestFilterAndRadius(t *testing.T) {
	g := New(100)
	g.Upsert(Item{ID: "busy", X: 0, Y: 0})
	g.Upsert(Item{ID: "free", X: 150, Y: 0})

	got, _ := g.NearestK(0, 0, 1000, 5, func(it Item) bool { return it.ID == "free" }, nil)
	if len(got) != 1 || got[0].ID != "free" {
		t.Fatalf("filter ignored: %+v", got)
	}

	all := g.Radius(0, 0, 200, nil)
	if len(all) != 2 {
		t.Fatalf("radius returned %d items, want 2", len(all))
	}
}

func BenchmarkNearestK72(b *testing.B) {
	g := New(450)
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 72; i++ {
		g.Upsert(Item{ID: fmt.Sprintf("d%d", i), X: rng.Float64()*13000 - 6500, Y: rng.Float64()*11000 - 5500})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x := rng.Float64()*13000 - 6500
		y := rng.Float64()*11000 - 5500
		g.NearestK(x, y, 7000, 18, nil, nil)
	}
}

func BenchmarkNearestK20k(b *testing.B) {
	g := New(450)
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 20000; i++ {
		g.Upsert(Item{ID: fmt.Sprintf("d%d", i), X: rng.Float64()*26000 - 13000, Y: rng.Float64()*22000 - 11000})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x := rng.Float64()*26000 - 13000
		y := rng.Float64()*22000 - 11000
		g.NearestK(x, y, 7000, 18, nil, nil)
	}
}

// BenchmarkLinearScan is the baseline the grid replaces: checking every driver.
func BenchmarkLinearScan20k(b *testing.B) {
	rng := rand.New(rand.NewSource(11))
	items := make([]refPoint, 20000)
	for i := range items {
		items[i] = refPoint{id: fmt.Sprintf("d%d", i), x: rng.Float64()*26000 - 13000, y: rng.Float64()*22000 - 11000}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x := rng.Float64()*26000 - 13000
		y := rng.Float64()*22000 - 11000
		best := make([]refPoint, 0, 18)
		for _, it := range items {
			if math.Hypot(it.x-x, it.y-y) <= 7000 {
				best = append(best, it)
			}
		}
		if len(best) > 18 {
			best = best[:18]
		}
	}
}
