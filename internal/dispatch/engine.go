package dispatch

import (
	"errors"
	"log"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"rahyab/internal/geo"
	"rahyab/internal/index"
)

// ErrOfferNotFound is returned when an offer id is unknown (already accepted,
// declined, expired or never existed).
var ErrOfferNotFound = errors.New("offer not found")

// ErrOfferExpired is returned when the rider took longer than OfferTTL.
var ErrOfferExpired = errors.New("offer expired")

// ErrDriverBusy is returned when the driver changed state between reservation
// and acceptance.
var ErrDriverBusy = errors.New("driver is no longer available")

// Config holds every tunable of the dispatch engine.
type Config struct {
	CellMeters           float64 `json:"cellMeters"`
	MaxSearchRadius      float64 `json:"maxSearchRadiusM"`
	MaxEtaSeconds        float64 `json:"maxEtaSeconds"`
	CandidatesPerRequest int     `json:"candidatesPerRequest"`
	PoolFactor           int     `json:"poolFactor"`
	BatchMs              int     `json:"batchMs"`
	OfferTTLSec          int     `json:"offerTtlSec"`
	MaxAttempts          int     `json:"maxAttempts"`
	Weights              Weights `json:"weights"`
}

// DefaultConfig returns the tuning used in the demo.
func DefaultConfig() Config {
	return Config{
		CellMeters:           450,
		MaxSearchRadius:      7000,
		MaxEtaSeconds:        900,
		CandidatesPerRequest: 6,
		PoolFactor:           3,
		BatchMs:              260,
		OfferTTLSec:          20,
		MaxAttempts:          3,
		Weights:              DefaultWeights(),
	}
}

func (c Config) batchInterval() time.Duration {
	if c.BatchMs < 40 {
		return 40 * time.Millisecond
	}
	return time.Duration(c.BatchMs) * time.Millisecond
}

func (c Config) offerTTL() time.Duration {
	if c.OfferTTLSec < 5 {
		return 5 * time.Second
	}
	return time.Duration(c.OfferTTLSec) * time.Second
}

// Request is one rider looking for a car.
type Request struct {
	ID         string      `json:"id"`
	Pickup     geo.Point   `json:"pickup"`
	Dropoff    geo.Point   `json:"dropoff"`
	CreatedAt  time.Time   `json:"createdAt"`
	Attempts   int         `json:"attempts"`
	Status     string      `json:"status"`
	Candidates []Candidate `json:"candidates"`
	OfferID    string      `json:"offerId,omitempty"`
	DriverID   string      `json:"driverId,omitempty"`
	EtaSec     float64     `json:"etaSec"`

	CellsScanned   int     `json:"cellsScanned"`
	DriversScanned int     `json:"driversScanned"`
	SearchTookMs   float64 `json:"searchTookMs"`
}

// Offer is a time-boxed proposal sent to one driver.
type Offer struct {
	ID         string    `json:"id"`
	RequestID  string    `json:"requestId"`
	DriverID   string    `json:"driverId"`
	DriverName string    `json:"driverName"`
	Car        string    `json:"car"`
	Plate      string    `json:"plate"`
	Rating     float64   `json:"rating"`
	EtaSec     float64   `json:"etaSec"`
	DistanceM  float64   `json:"distanceM"`
	Cost       float64   `json:"cost"`
	Pickup     geo.Point `json:"pickup"`
	Dropoff    geo.Point `json:"dropoff"`
	CreatedAt  time.Time `json:"createdAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// Engine owns the matching loop. All state mutations of the fleet go through
// the Store; the engine only serialises requests, batches and offers, which
// keeps the locking story short enough to reason about.
type Engine struct {
	cfg    Config
	store  *Store
	proj   geo.Projector
	router geo.Router
	bus    *Bus
	stats  *metrics

	jobs chan *Request
	stop chan struct{}
	once sync.Once

	mu       sync.Mutex
	pending  []*Request
	offers   map[string]*Offer
	requests map[string]*Request

	autoAccept atomic.Bool
}

// NewEngine wires the dispatcher.
func NewEngine(cfg Config, store *Store, router geo.Router, bus *Bus, proj geo.Projector) *Engine {
	return &Engine{
		cfg:      cfg,
		store:    store,
		proj:     proj,
		router:   router,
		bus:      bus,
		stats:    &metrics{},
		jobs:     make(chan *Request, 1024),
		stop:     make(chan struct{}),
		offers:   make(map[string]*Offer),
		requests: make(map[string]*Request),
	}
}

// Start launches the single dispatch goroutine.
func (e *Engine) Start() {
	go e.run()
}

// Stop terminates the dispatch loop.
func (e *Engine) Stop() {
	e.once.Do(func() { close(e.stop) })
}

func (e *Engine) run() {
	batch := time.NewTicker(e.cfg.batchInterval())
	sweep := time.NewTicker(150 * time.Millisecond)
	defer batch.Stop()
	defer sweep.Stop()

	for {
		select {
		case <-e.stop:
			return
		case req := <-e.jobs:
			e.enqueue(req)
		case <-batch.C:
			e.matchBatch()
		case <-sweep.C:
			e.sweepOffers()
			e.gcRequests()
		}
	}
}

func (e *Engine) enqueue(req *Request) {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now()
	}
	e.mu.Lock()
	req.Status = "queued"
	e.pending = append(e.pending, req)
	e.requests[req.ID] = req
	e.mu.Unlock()

	// Retries pass through the same queue; only the first attempt is a new
	// request and should be announced as one.
	if req.Attempts == 0 {
		e.bus.Publish("request.created", map[string]any{
			"requestId": req.ID,
			"pickup":    req.Pickup,
			"dropoff":   req.Dropoff,
			"at":        req.CreatedAt,
		})
	}
}

// Submit accepts a ride request. The returns value is only the id; the caller
// must not hold on to the request pointer because the engine mutates it.
func (e *Engine) Submit(pickup, dropoff geo.Point) (string, error) {
	if pickup.IsZero() || dropoff.IsZero() {
		return "", errors.New("pickup and dropoff are required")
	}
	if geo.DistanceMeters(pickup, dropoff) < 60 {
		return "", errors.New("pickup and dropoff are too close")
	}
	req := &Request{ID: nextID('r'), Pickup: pickup, Dropoff: dropoff, CreatedAt: time.Now()}
	select {
	case e.jobs <- req:
	default:
		return "", errors.New("dispatch queue is full")
	}
	e.stats.incRequest()
	return req.ID, nil
}

type matchRow struct {
	req    *Request
	scored []Candidate // full pool, used for the assignment
	trace  index.Stats
	ms     float64
}

func (e *Engine) matchBatch() {
	e.mu.Lock()
	if len(e.pending) == 0 {
		e.mu.Unlock()
		return
	}
	batch := make([]*Request, len(e.pending))
	copy(batch, e.pending)
	e.pending = e.pending[:0]
	e.mu.Unlock()

	now := time.Now()
	started := time.Now()

	rows := make([]*matchRow, 0, len(batch))
	colOf := make(map[string]int, len(batch)*4)
	union := make([]string, 0, len(batch)*4)

	pool := e.cfg.CandidatesPerRequest * e.cfg.PoolFactor
	if pool < 4 {
		pool = 4
	}

	for _, req := range batch {
		req.Attempts++
		req.Status = "searching"

		queryStart := time.Now()
		drivers, st := e.store.SearchAvailable(req.Pickup, e.cfg.MaxSearchRadius, pool, nil,
			func(ring, cells, found int) {
				e.bus.Publish("search.ring", map[string]any{
					"requestId": req.ID,
					"ring":      ring,
					"cells":     cells,
					"found":     found,
				})
			})
		ms := elapsedMs(time.Since(queryStart))

		scored := make([]Candidate, 0, len(drivers))
		for _, d := range drivers {
			c := scoreDriver(d, req.Pickup, now, e.router, e.cfg.Weights)
			if c.EtaSec > e.cfg.MaxEtaSeconds {
				continue
			}
			scored = append(scored, c)
		}
		sort.SliceStable(scored, func(i, j int) bool {
			if scored[i].Cost == scored[j].Cost {
				return scored[i].DriverID < scored[j].DriverID
			}
			return scored[i].Cost < scored[j].Cost
		})

		req.CellsScanned = st.Cells
		req.DriversScanned = st.Scanned
		req.SearchTookMs = round2(ms)
		shown := scored
		if len(shown) > e.cfg.CandidatesPerRequest {
			shown = shown[:e.cfg.CandidatesPerRequest]
		}
		req.Candidates = shown

		rows = append(rows, &matchRow{req: req, scored: scored, trace: st, ms: ms})
		e.stats.observeSearch(ms, st.Cells, st.Scanned, firstEta(scored))

		for _, c := range scored {
			if _, ok := colOf[c.DriverID]; !ok {
				colOf[c.DriverID] = len(union)
				union = append(union, c.DriverID)
			}
		}

		e.bus.Publish("search.done", map[string]any{
			"requestId":  req.ID,
			"candidates": shown,
			"cells":      st.Cells,
			"rings":      st.Rings,
			"scanned":    st.Scanned,
			"ms":         round2(ms),
			"attempt":    req.Attempts,
		})
	}

	if len(union) == 0 {
		for _, r := range rows {
			e.unmatched(r.req, "no_drivers_in_radius")
		}
		e.bus.Publish("batch", map[string]any{
			"requests":     len(rows),
			"drivers":      0,
			"matched":      0,
			"assignmentMs": 0,
			"totalMs":      round2(elapsedMs(time.Since(started))),
			"method":       "hungarian",
		})
		return
	}

	cost := make([][]float64, len(rows))
	for i, r := range rows {
		cost[i] = make([]float64, len(union))
		for j := range cost[i] {
			cost[i][j] = bigM
		}
		for _, c := range r.scored {
			cost[i][colOf[c.DriverID]] = c.Cost
		}
	}

	assignStart := time.Now()
	assign := Solve(cost)
	assignMs := elapsedMs(time.Since(assignStart))
	e.stats.observeAssign(assignMs)

	matched := 0
	for i, r := range rows {
		if assign[i] < 0 {
			e.unmatched(r.req, "no_suitable_driver")
			continue
		}
		driverID := union[assign[i]]
		cand, ok := findCandidate(r.scored, driverID)
		if !ok {
			e.unmatched(r.req, "no_suitable_driver")
			continue
		}
		e.createOffer(r.req, cand, now)
		matched++
	}

	e.bus.Publish("batch", map[string]any{
		"requests":     len(rows),
		"drivers":      len(union),
		"matched":      matched,
		"assignmentMs": round2(assignMs),
		"totalMs":      round2(elapsedMs(time.Since(started))),
		"method":       "hungarian",
	})
}

func firstEta(c []Candidate) float64 {
	if len(c) == 0 {
		return 0
	}
	return c[0].EtaSec
}

func findCandidate(list []Candidate, id string) (Candidate, bool) {
	for _, c := range list {
		if c.DriverID == id {
			return c, true
		}
	}
	return Candidate{}, false
}

func (e *Engine) createOffer(req *Request, c Candidate, now time.Time) {
	offerID := nextID('o')
	drv, ok := e.store.Reserve(c.DriverID, offerID)
	if !ok {
		e.unmatched(req, "driver_taken")
		return
	}

	off := &Offer{
		ID:         offerID,
		RequestID:  req.ID,
		DriverID:   drv.ID,
		DriverName: drv.Name,
		Car:        drv.Car,
		Plate:      drv.Plate,
		Rating:     drv.Rating,
		EtaSec:     round1(c.EtaSec),
		DistanceM:  c.DistanceM,
		Cost:       round1(c.Cost),
		Pickup:     req.Pickup,
		Dropoff:    req.Dropoff,
		CreatedAt:  now,
		ExpiresAt:  now.Add(e.cfg.offerTTL()),
	}

	e.mu.Lock()
	e.offers[offerID] = off
	req.Status = "offered"
	req.OfferID = offerID
	req.DriverID = drv.ID
	req.EtaSec = off.EtaSec
	e.mu.Unlock()

	e.stats.incMatched()
	e.stats.incOffer()
	e.bus.Publish("offer.created", off)

	if e.autoAccept.Load() {
		delay := time.Duration(1100+rand.Intn(2400)) * time.Millisecond
		time.AfterFunc(delay, func() {
			if _, err := e.Accept(offerID); err != nil {
				// Expiry or a manual decline already handled it.
				return
			}
		})
	}
}

// Accept confirms an offer and opens the trip.
func (e *Engine) Accept(offerID string) (*Trip, error) {
	e.mu.Lock()
	off, ok := e.offers[offerID]
	if !ok {
		e.mu.Unlock()
		return nil, ErrOfferNotFound
	}
	delete(e.offers, offerID)
	req := e.requests[off.RequestID]
	e.mu.Unlock()

	now := time.Now()
	if now.After(off.ExpiresAt) {
		e.store.Release(off.DriverID, off.ID)
		e.stats.incExpired()
		e.bus.Publish("offer.expired", map[string]any{
			"offerId":   off.ID,
			"driverId":  off.DriverID,
			"requestId": off.RequestID,
		})
		if req != nil {
			e.unmatched(req, "offer_expired")
		}
		return nil, ErrOfferExpired
	}

	distance := e.router.Distance(off.Pickup, off.Dropoff)
	etaToDrop := e.router.ETA(off.Pickup, off.Dropoff, now)
	trip := Trip{
		ID:        nextID('t'),
		RequestID: off.RequestID,
		DriverID:  off.DriverID,
		Pickup:    off.Pickup,
		Dropoff:   off.Dropoff,
		Phase:     PhaseToPickup,
		StartedAt: now,
		DistanceM: distance,
		Fare:      fareFor(distance, etaToDrop),
	}

	drv, ok := e.store.StartTrip(off.DriverID, off.ID, trip)
	if !ok {
		return nil, ErrDriverBusy
	}

	if req != nil {
		req.Status = "on_trip"
	}

	e.stats.incAccepted()
	e.bus.Publish("offer.accepted", map[string]any{
		"offerId":    off.ID,
		"requestId":  off.RequestID,
		"driverId":   drv.ID,
		"driverName": drv.Name,
		"car":        drv.Car,
		"plate":      drv.Plate,
		"etaSec":     off.EtaSec,
	})
	e.bus.Publish("trip.started", trip)

	return &trip, nil
}

// Decline rejects an offer and returns the request to the dispatch queue.
func (e *Engine) Decline(offerID string) error {
	e.mu.Lock()
	off, ok := e.offers[offerID]
	if !ok {
		e.mu.Unlock()
		return ErrOfferNotFound
	}
	delete(e.offers, offerID)
	req := e.requests[off.RequestID]
	e.mu.Unlock()

	e.store.Release(off.DriverID, off.ID)
	e.stats.incDeclined()
	e.bus.Publish("offer.declined", map[string]any{
		"offerId":   off.ID,
		"driverId":  off.DriverID,
		"requestId": off.RequestID,
	})
	if req != nil {
		e.unmatched(req, "driver_declined")
	}
	return nil
}

func (e *Engine) sweepOffers() {
	now := time.Now()
	var expired []*Offer

	e.mu.Lock()
	for id, off := range e.offers {
		if now.After(off.ExpiresAt) {
			expired = append(expired, off)
			delete(e.offers, id)
		}
	}
	e.mu.Unlock()

	for _, off := range expired {
		e.store.Release(off.DriverID, off.ID)
		e.stats.incExpired()
		e.bus.Publish("offer.expired", map[string]any{
			"offerId":   off.ID,
			"driverId":  off.DriverID,
			"requestId": off.RequestID,
		})
		e.mu.Lock()
		req := e.requests[off.RequestID]
		e.mu.Unlock()
		if req != nil {
			e.unmatched(req, "offer_expired")
		}
	}
}

// unmatched retries a request with a short backoff until MaxAttempts, then
// gives up and reports it. Retrying matters in production: a driver may free
// up 400 ms later, and failing the rider immediately would be wrong.
func (e *Engine) unmatched(req *Request, reason string) {
	if req.Attempts < e.cfg.MaxAttempts {
		req.Status = "retrying"
		attempt := req.Attempts
		e.bus.Publish("request.retry", map[string]any{
			"requestId": req.ID,
			"reason":    reason,
			"attempt":   attempt,
		})
		delay := time.Duration(400+attempt*250) * time.Millisecond
		time.AfterFunc(delay, func() {
			select {
			case e.jobs <- req:
			case <-e.stop:
			}
		})
		return
	}

	req.Status = "unmatched"
	e.stats.incUnmatched()
	e.bus.Publish("request.unmatched", map[string]any{
		"requestId": req.ID,
		"reason":    reason,
		"attempts":  req.Attempts,
	})
}

func (e *Engine) gcRequests() {
	cutoff := time.Now().Add(-2 * time.Minute)
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, req := range e.requests {
		if req.Status == "unmatched" && req.CreatedAt.Before(cutoff) {
			delete(e.requests, id)
		}
	}
}

// Fleet returns a snapshot for the map and the fleet table.
func (e *Engine) Fleet() []Driver { return e.store.Snapshot() }

// ConfigSnapshot returns the active tuning.
func (e *Engine) ConfigSnapshot() Config { return e.cfg }

// SetAutoAccept toggles simulated driver acceptance.
func (e *Engine) SetAutoAccept(v bool) { e.autoAccept.Store(v) }

// AutoAccept reports the current setting.
func (e *Engine) AutoAccept() bool { return e.autoAccept.Load() }

// Stats merges fleet counters with latency statistics.
func (e *Engine) Stats() StatsSnapshot {
	total, available, reserved, onTrip := e.store.Counts()
	s := e.stats.snapshot()
	s.LiveSessions, s.Dropped = e.bus.Stats()

	out := StatsSnapshot{
		Drivers:      total,
		Available:    available,
		Reserved:     reserved,
		OnTrip:       onTrip,
		Requests:     s.Requests,
		Matched:      s.Matched,
		Unmatched:    s.Unmatched,
		Offers:       s.Offers,
		Accepted:     s.Accepted,
		Declined:     s.Declined,
		Expired:      s.Expired,
		AcceptRate:   s.AcceptRate,
		AvgSearchMs:  s.AvgSearchMs,
		AvgSearchUs:  s.AvgSearchUs,
		P95SearchMs:  s.P95SearchMs,
		AvgAssignMs:  s.AvgAssignMs,
		AvgCells:     s.AvgCells,
		AvgScanned:   s.AvgScanned,
		AvgEtaSec:    s.AvgEtaSec,
		LiveSessions: s.LiveSessions,
		Dropped:      s.Dropped,
	}
	// Fleet counts are exposed through the fleet endpoint; embed them here too
	// so a single poll can render the whole dashboard header.
	return out
}

// Reset clears all runtime state and replaces the fleet.
func (e *Engine) Reset(drivers []Driver) {
	e.mu.Lock()
	e.pending = nil
	e.offers = make(map[string]*Offer)
	e.requests = make(map[string]*Request)
	e.mu.Unlock()

	for {
		select {
		case <-e.jobs:
			continue
		default:
		}
		break
	}

	e.store.Reset(drivers)
	e.stats.reset()
	e.bus.Publish("fleet.reset", map[string]any{"drivers": len(drivers), "at": time.Now()})
	log.Printf("dispatch: fleet reset with %d drivers", len(drivers))
}

// fareFor converts distance and duration into a plausible metered fare in
// Toman (base + per-km + per-minute, rounded to the nearest thousand).
func fareFor(distanceM, etaSec float64) int {
	base := 30000.0
	perKm := 8500.0
	perMin := 900.0
	f := base + (distanceM/1000)*perKm + (etaSec/60)*perMin
	return int(f/1000+0.5) * 1000
}
