// Package index implements the spatial index used to answer "who is the
// closest driver?" without scanning the fleet.
//
// The structure is a uniform grid of square cells (the same idea as a geohash
// prefix or an H3 cell, but with a plain square lattice: cheaper to compute and
// exact at short range, which is all the dispatcher needs). Each cell keeps a
// dense slice of live positions, and a query expands ring by ring around the
// pickup cell, maintaining a bounded max-heap of the K nearest drivers.
package index

import (
	"container/heap"
	"math"
	"sync"
)

// Item is a point stored in the grid.
type Item struct {
	ID string
	X  float64 // metres east
	Y  float64 // metres north
}

// Stats describes the work a single query performed.
type Stats struct {
	Rings     int `json:"rings"`
	Cells     int `json:"cells"`
	Scanned   int `json:"scanned"`
	ResultSet int `json:"resultSet"`
}

// Grid is a concurrency-safe uniform spatial grid.
type Grid struct {
	mu    sync.RWMutex
	cell  float64
	cells map[int64][]Item
	where map[string]int64
}

// New creates a grid whose cells are cellMeters wide.
func New(cellMeters float64) *Grid {
	if cellMeters <= 0 {
		cellMeters = 400
	}
	return &Grid{
		cell:  cellMeters,
		cells: make(map[int64][]Item, 1024),
		where: make(map[string]int64, 1024),
	}
}

// CellMeters returns the configured cell size.
func (g *Grid) CellMeters() float64 { return g.cell }

// Len returns the number of indexed items.
func (g *Grid) Len() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.where)
}

func (g *Grid) coord(x, y float64) (int, int) {
	return int(math.Floor(x / g.cell)), int(math.Floor(y / g.cell))
}

// key packs two cell coordinates into one int64 map key. City-scale
// coordinates fit comfortably in 32 bits each.
func key(cx, cy int) int64 {
	return int64(int32(cx))<<32 | int64(int64(uint32(int32(cy))))
}

// Upsert inserts or moves an item.
func (g *Grid) Upsert(it Item) {
	cx, cy := g.coord(it.X, it.Y)
	k := key(cx, cy)

	g.mu.Lock()
	defer g.mu.Unlock()

	if old, ok := g.where[it.ID]; ok {
		if old == k {
			// Same cell: replace in place, no bookkeeping needed.
			bucket := g.cells[k]
			for i := range bucket {
				if bucket[i].ID == it.ID {
					bucket[i] = it
					return
				}
			}
		}
		g.detach(it.ID, old)
	}

	g.cells[k] = append(g.cells[k], it)
	g.where[it.ID] = k
}

// Remove deletes an item. Removing an unknown id is a no-op.
func (g *Grid) Remove(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if k, ok := g.where[id]; ok {
		g.detach(id, k)
	}
}

// detach must be called with the write lock held.
func (g *Grid) detach(id string, k int64) {
	bucket := g.cells[k]
	for i := range bucket {
		if bucket[i].ID == id {
			bucket[i] = bucket[len(bucket)-1]
			bucket = bucket[:len(bucket)-1]
			if len(bucket) == 0 {
				delete(g.cells, k)
			} else {
				g.cells[k] = bucket
			}
			break
		}
	}
	delete(g.where, id)
}

// NearestK returns up to k items within maxR metres of (x,y), ordered by
// distance, and reports how many cells were touched.
//
// Correctness of the early exit: after finishing ring r, every cell that has
// not been visited yet is at least r cells away in Chebyshev distance, so its
// nearest point is at least r*cell metres away from the query point. If the
// K-th best distance so far is smaller than that bound, no unvisited cell can
// contain a closer driver and the search stops.
func (g *Grid) NearestK(x, y, maxR float64, k int, filter func(Item) bool, onRing func(ring, cells, found int)) ([]Item, Stats) {
	if k <= 0 {
		return nil, Stats{}
	}
	g.mu.RLock()
	defer g.mu.RUnlock()

	cx, cy := g.coord(x, y)
	maxRing := int(math.Ceil(maxR / g.cell))
	if maxRing < 1 {
		maxRing = 1
	}

	st := Stats{}
	h := &maxHeap{}
	heap.Init(h)

	for ring := 0; ring <= maxRing; ring++ {
		cells, found := 0, 0
		for dy := -ring; dy <= ring; dy++ {
			for dx := -ring; dx <= ring; dx++ {
				if maxAbs(dx, dy) != ring {
					continue // interior rings were scanned already
				}
				cells++
				bucket := g.cells[key(cx+dx, cy+dy)]
				for _, it := range bucket {
					st.Scanned++
					if filter != nil && !filter(it) {
						continue
					}
					d := math.Hypot(it.X-x, it.Y-y)
					if d > maxR {
						continue
					}
					itCopy := it
					if h.push(heapEntry{dist: d, item: itCopy}, k) {
						found++
					}
				}
			}
		}
		st.Cells += cells
		st.Rings = ring + 1
		if onRing != nil {
			onRing(ring, cells, found)
		}
		if h.Len() == k && h.peek().dist <= float64(ring)*g.cell {
			break
		}
	}

	out := h.ascending()
	st.ResultSet = len(out)
	return out, st
}

// Radius returns every item within r metres. Used by tooling and tests.
func (g *Grid) Radius(x, y, r float64, filter func(Item) bool) []Item {
	g.mu.RLock()
	defer g.mu.RUnlock()

	cx, cy := g.coord(x, y)
	rings := int(math.Ceil(r / g.cell))
	out := make([]Item, 0, 16)
	for dy := -rings; dy <= rings; dy++ {
		for dx := -rings; dx <= rings; dx++ {
			for _, it := range g.cells[key(cx+dx, cy+dy)] {
				if filter != nil && !filter(it) {
					continue
				}
				if math.Hypot(it.X-x, it.Y-y) <= r {
					out = append(out, it)
				}
			}
		}
	}
	return out
}

func maxAbs(a, b int) int {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	if a > b {
		return a
	}
	return b
}

type heapEntry struct {
	dist float64
	item Item
}

// maxHeap keeps the k smallest distances seen so far; the root is the worst
// of them, which is exactly the value needed for the early-exit test.
type maxHeap []heapEntry

func (h maxHeap) Len() int           { return len(h) }
func (h maxHeap) Less(i, j int) bool { return h[i].dist > h[j].dist }
func (h maxHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *maxHeap) Push(v any)        { *h = append(*h, v.(heapEntry)) }
func (h *maxHeap) Pop() any          { old := *h; n := len(old); it := old[n-1]; *h = old[:n-1]; return it }
func (h *maxHeap) peek() heapEntry   { return (*h)[0] }
func (h *maxHeap) push(e heapEntry, k int) bool {
	if len(*h) < k {
		heap.Push(h, e)
		return true
	}
	if e.dist < h.peek().dist {
		(*h)[0] = e
		heap.Fix(h, 0)
		return true
	}
	return false
}

func (h *maxHeap) ascending() []Item {
	n := h.Len()
	out := make([]Item, n)
	for i := n - 1; i >= 0; i-- {
		out[i] = heap.Pop(h).(heapEntry).item
	}
	return out
}
