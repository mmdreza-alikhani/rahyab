package dispatch

import (
	"sync"
	"time"

	"rahyab/internal/geo"
	"rahyab/internal/index"
)

// Store owns the mutable fleet. Reads take a read lock, state transitions take
// the write lock, and every state change is validated against the current
// state so that a late accept from one HTTP request cannot steal a driver that
// is already on another trip.
type Store struct {
	mu      sync.RWMutex
	proj    geo.Projector
	grid    *index.Grid
	drivers map[string]*Driver
	order   []string
}

// NewStore creates an empty fleet with the given projection and cell size.
func NewStore(proj geo.Projector, cellMeters float64) *Store {
	return &Store{
		proj:    proj,
		grid:    index.New(cellMeters),
		drivers: make(map[string]*Driver, 256),
	}
}

// Add inserts a driver and indexes its position.
func (s *Store) Add(d Driver) {
	if d.State == "" {
		d.State = StateAvailable
	}
	if d.IdleSince.IsZero() {
		d.IdleSince = time.Now()
	}
	cp := d

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.drivers[d.ID]; !exists {
		s.order = append(s.order, d.ID)
	}
	s.drivers[d.ID] = &cp
	x, y := s.proj.XY(d.Pos)
	s.grid.Upsert(index.Item{ID: d.ID, X: x, Y: y})
}

// Get returns a copy of a driver.
func (s *Store) Get(id string) (Driver, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.drivers[id]
	if !ok {
		return Driver{}, false
	}
	return *d, true
}

// Snapshot returns a stable copy of the whole fleet in insertion order.
func (s *Store) Snapshot() []Driver {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Driver, 0, len(s.order))
	for _, id := range s.order {
		if d, ok := s.drivers[id]; ok {
			out = append(out, *d)
		}
	}
	return out
}

// Counts returns how many drivers are in each state.
func (s *Store) Counts() (total, available, reserved, onTrip int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.drivers {
		switch d.State {
		case StateAvailable:
			available++
		case StateReserved:
			reserved++
		case StateOnTrip:
			onTrip++
		}
	}
	return len(s.drivers), available, reserved, onTrip
}

// Move updates a driver position and heading. It is safe to call at tick rate.
func (s *Store) Move(id string, p geo.Point, heading float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drivers[id]
	if !ok {
		return
	}
	d.Pos = p
	d.Heading = heading
	x, y := s.proj.XY(p)
	s.grid.Upsert(index.Item{ID: id, X: x, Y: y})
}

// Reserve atomically moves a driver from available to reserved for one offer.
func (s *Store) Reserve(id, offerID string) (Driver, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drivers[id]
	if !ok || d.State != StateAvailable {
		return Driver{}, false
	}
	d.State = StateReserved
	d.OfferID = offerID
	return *d, true
}

// Release returns a reserved driver to the pool. The offer id check makes the
// operation idempotent and prevents an expired offer from releasing a driver
// that has meanwhile accepted a different request.
func (s *Store) Release(id, offerID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drivers[id]
	if !ok || d.State != StateReserved {
		return false
	}
	if offerID != "" && d.OfferID != offerID {
		return false
	}
	d.State = StateAvailable
	d.OfferID = ""
	d.IdleSince = time.Now()
	return true
}

// StartTrip commits a reserved driver to a trip.
func (s *Store) StartTrip(id, offerID string, tr Trip) (Driver, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drivers[id]
	if !ok || d.State != StateReserved || d.OfferID != offerID {
		return Driver{}, false
	}
	tripCopy := tr
	if tripCopy.Phase == "" {
		tripCopy.Phase = PhaseToPickup
	}
	d.State = StateOnTrip
	d.OfferID = ""
	d.Trip = &tripCopy
	d.Trips++
	return *d, true
}

// MarkPickedUp advances the trip to its second leg. It is idempotent: calling
// it twice leaves the phase unchanged.
func (s *Store) MarkPickedUp(id string) (Trip, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drivers[id]
	if !ok || d.State != StateOnTrip || d.Trip == nil {
		return Trip{}, false
	}
	d.Trip.Phase = PhaseToDropoff
	return *d.Trip, true
}

// CompleteTrip frees an on-trip driver and returns the finished trip.
func (s *Store) CompleteTrip(id string) (Driver, Trip, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drivers[id]
	if !ok || d.State != StateOnTrip || d.Trip == nil {
		return Driver{}, Trip{}, false
	}
	tr := *d.Trip
	d.State = StateAvailable
	d.Trip = nil
	d.OfferID = ""
	d.IdleSince = time.Now()
	return *d, tr, true
}

// SearchAvailable returns up to k available drivers around p, closest first by
// crow flight, together with the index statistics of the query.
func (s *Store) SearchAvailable(p geo.Point, maxR float64, k int, excluded map[string]bool, onRing func(ring, cells, found int)) ([]Driver, index.Stats) {
	x, y := s.proj.XY(p)

	s.mu.RLock()
	defer s.mu.RUnlock()

	items, st := s.grid.NearestK(x, y, maxR, k, func(it index.Item) bool {
		d, ok := s.drivers[it.ID]
		if !ok || d.State != StateAvailable {
			return false
		}
		return !excluded[it.ID]
	}, onRing)

	out := make([]Driver, 0, len(items))
	for _, it := range items {
		if d, ok := s.drivers[it.ID]; ok {
			out = append(out, *d)
		}
	}
	return out, st
}

// Reset replaces the whole fleet. Used by the demo reset endpoint.
func (s *Store) Reset(drivers []Driver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drivers = make(map[string]*Driver, len(drivers))
	s.order = s.order[:0]
	s.grid = index.New(s.grid.CellMeters())
	for _, d := range drivers {
		if d.State == "" {
			d.State = StateAvailable
		}
		if d.IdleSince.IsZero() {
			d.IdleSince = time.Now()
		}
		cp := d
		s.drivers[d.ID] = &cp
		s.order = append(s.order, d.ID)
		x, y := s.proj.XY(d.Pos)
		s.grid.Upsert(index.Item{ID: d.ID, X: x, Y: y})
	}
}
