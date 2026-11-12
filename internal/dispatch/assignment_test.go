package dispatch

import (
	"math"
	"math/rand"
	"testing"
)

func totalCost(cost [][]float64, assign []int) float64 {
	var sum float64
	for i, j := range assign {
		if j < 0 {
			sum += bigM
			continue
		}
		sum += cost[i][j]
	}
	return sum
}

// bruteAssign enumerates every injective row->column mapping. Only usable for
// tiny matrices, which is exactly what a test needs.
func bruteAssign(cost [][]float64) float64 {
	rows := len(cost)
	cols := len(cost[0])
	used := make([]bool, cols)
	best := math.Inf(1)

	var walk func(i int, sum float64)
	walk = func(i int, sum float64) {
		if i == rows {
			if sum < best {
				best = sum
			}
			return
		}
		for j := 0; j < cols; j++ {
			if used[j] || cost[i][j] >= bigM/2 {
				continue
			}
			used[j] = true
			walk(i+1, sum+cost[i][j])
			used[j] = false
		}
		// Leaving the row without a real column costs a dummy, exactly like
		// the padded matrix the solver works on.
		walk(i+1, sum+bigM)
	}
	walk(0, 0)
	return best
}

func TestSolveMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 300; trial++ {
		rows := 1 + rng.Intn(5)
		cols := 1 + rng.Intn(5)
		cost := make([][]float64, rows)
		for i := range cost {
			cost[i] = make([]float64, cols)
			for j := range cost[i] {
				if rng.Float64() < 0.2 {
					cost[i][j] = bigM
				} else {
					cost[i][j] = math.Round(rng.Float64()*400*10) / 10
				}
			}
		}

		got := Solve(cost)
		gotTotal := totalCost(cost, got)
		wantTotal := bruteAssign(cost)

		if math.Abs(gotTotal-wantTotal) > 1e-6 {
			t.Fatalf("trial %d: optimal cost %.2f, got %.2f (rows=%d cols=%d)\n%v", trial, wantTotal, gotTotal, rows, cols, cost)
		}

		seen := map[int]bool{}
		for _, j := range got {
			if j < 0 {
				continue
			}
			if seen[j] {
				t.Fatalf("column %d assigned twice: %v", j, got)
			}
			seen[j] = true
		}
	}
}

func TestGreedyLosesToOptimal(t *testing.T) {
	// Row 0 taking its local favourite blocks row 1 from the only cheap pair.
	cost := [][]float64{
		{1, 2},
		{1, 100},
	}
	greedy := SolveGreedy(cost)
	optimal := Solve(cost)

	if totalCost(cost, greedy) != 101 {
		t.Fatalf("greedy baseline changed: %.1f", totalCost(cost, greedy))
	}
	if totalCost(cost, optimal) != 3 {
		t.Fatalf("optimal assignment should cost 3, got %.1f", totalCost(cost, optimal))
	}
}

func TestSolveHandlesEmptyAndRectangular(t *testing.T) {
	if got := Solve(nil); got != nil {
		t.Fatalf("empty matrix should return nil, got %v", got)
	}
	// More rows than columns: only one pair can be served.
	cost := [][]float64{{10}, {20}, {30}}
	got := Solve(cost)
	assigned := 0
	for _, j := range got {
		if j >= 0 {
			assigned++
		}
	}
	if assigned != 1 {
		t.Fatalf("expected exactly one assignment, got %v", got)
	}
	if got[0] != 0 {
		t.Fatalf("cheapest row must win the single column, got %v", got)
	}
}

func BenchmarkSolve40x60(b *testing.B) {
	rng := rand.New(rand.NewSource(5))
	cost := make([][]float64, 40)
	for i := range cost {
		cost[i] = make([]float64, 60)
		for j := range cost[i] {
			cost[i][j] = 40 + rng.Float64()*360
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Solve(cost)
	}
}
