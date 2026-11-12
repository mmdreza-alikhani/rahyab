package dispatch

import "math"

// bigM is the cost of an infeasible pair. It is finite so that the assignment
// stays a well-formed square problem, but large enough that the solver only
// picks it when a row truly has no candidate left. Real costs are on the order
// of a few hundred seconds, so 1e6 is safely out of reach.
const bigM = 1e6

// Solve computes the minimum-cost assignment of rows to columns.
//
// It runs the Hungarian method (Kuhn-Munkres with potentials, O(n^3)) on a
// square matrix. Missing pairs are padded with bigM, and the returned slice
// maps each row to a column, or -1 when the row stayed unassigned.
//
// Why the optimal assignment instead of "closest driver first": a greedy pass
// produces a maximal matching, not a minimum-cost one. Two riders arriving in
// the same batch can both be closest to the same car; greedy gives it to
// whoever is processed first and pushes the second rider to a far driver,
// raising total pickup time. The Hungarian method minimises the sum over the
// whole batch, which is exactly the objective the marketplace cares about.
func Solve(cost [][]float64) []int {
	rows := len(cost)
	if rows == 0 {
		return nil
	}
	cols := len(cost[0])
	for _, r := range cost {
		if len(r) != cols {
			panic("dispatch: ragged cost matrix")
		}
	}

	n := rows
	if cols > n {
		n = cols
	}

	a := make([][]float64, n)
	for i := range a {
		a[i] = make([]float64, n)
		for j := range a[i] {
			switch {
			case i < rows && j < cols:
				a[i][j] = cost[i][j]
			default:
				a[i][j] = bigM
			}
		}
	}

	const inf = math.MaxFloat64 / 4

	u := make([]float64, n+1)
	v := make([]float64, n+1)
	p := make([]int, n+1)   // p[j] = row matched to column j
	way := make([]int, n+1) // predecessor column for augmenting path

	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		minv := make([]float64, n+1)
		used := make([]bool, n+1)
		for j := 0; j <= n; j++ {
			minv[j] = inf
		}

		for {
			used[j0] = true
			i0 := p[j0]
			delta := inf
			j1 := 0
			for j := 1; j <= n; j++ {
				if used[j] {
					continue
				}
				cur := a[i0-1][j-1] - u[i0] - v[j]
				if cur < minv[j] {
					minv[j] = cur
					way[j] = j0
				}
				if minv[j] < delta {
					delta = minv[j]
					j1 = j
				}
			}
			for j := 0; j <= n; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}

		for {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
			if j0 == 0 {
				break
			}
		}
	}

	res := make([]int, rows)
	for i := range res {
		res[i] = -1
	}
	for j := 1; j <= n; j++ {
		i := p[j] - 1
		c := j - 1
		if i >= 0 && i < rows && c < cols && a[i][c] < bigM/2 {
			res[i] = c
		}
	}
	return res
}

// SolveGreedy is the naive baseline used by benchmarks and by the README to
// quantify how much the optimal batch assignment actually buys. It assigns
// rows in order to their cheapest still-free column.
func SolveGreedy(cost [][]float64) []int {
	rows := len(cost)
	if rows == 0 {
		return nil
	}
	cols := len(cost[0])
	res := make([]int, rows)
	taken := make([]bool, cols)
	for i := range res {
		res[i] = -1
		best, bestJ := bigM, -1
		for j := 0; j < cols; j++ {
			if taken[j] {
				continue
			}
			if cost[i][j] < best {
				best, bestJ = cost[i][j], j
			}
		}
		if bestJ >= 0 && best < bigM/2 {
			res[i] = bestJ
			taken[bestJ] = true
		}
	}
	return res
}
