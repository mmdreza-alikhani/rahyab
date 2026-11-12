package dispatch

import (
	"sort"
	"sync"
)

// metrics keeps the operational numbers shown in the dashboard. Latency
// samples are capped so that a long-running process cannot grow without bound;
// p95 over the last few hundred matches is what an operator actually cares
// about, not a lifetime average.
type metrics struct {
	mu sync.Mutex

	requests  int
	matched   int
	unmatched int
	offers    int
	accepted  int
	declined  int
	expired   int

	searchMs []float64
	assignMs []float64
	cells    []float64
	scanned  []float64
	etas     []float64
}

const sampleCap = 512

func (m *metrics) observeSearch(ms float64, cellsSeen, driversSeen int, eta float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searchMs = push(m.searchMs, ms)
	if cellsSeen > 0 {
		m.cells = push(m.cells, float64(cellsSeen))
	}
	if driversSeen > 0 {
		m.scanned = push(m.scanned, float64(driversSeen))
	}
	if eta > 0 {
		m.etas = push(m.etas, eta)
	}
}

func (m *metrics) observeAssign(ms float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.assignMs = push(m.assignMs, ms)
}

func push(s []float64, v float64) []float64 {
	if len(s) >= sampleCap {
		copy(s, s[1:])
		s[len(s)-1] = v
		return s
	}
	return append(s, v)
}

func (m *metrics) incRequest()   { m.add(func() { m.requests++ }) }
func (m *metrics) incMatched()   { m.add(func() { m.matched++ }) }
func (m *metrics) incUnmatched() { m.add(func() { m.unmatched++ }) }
func (m *metrics) incOffer()     { m.add(func() { m.offers++ }) }
func (m *metrics) incAccepted()  { m.add(func() { m.accepted++ }) }
func (m *metrics) incDeclined()  { m.add(func() { m.declined++ }) }
func (m *metrics) incExpired()   { m.add(func() { m.expired++ }) }

func (m *metrics) add(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn()
}

// StatsSnapshot is the JSON payload of /api/stats.
type StatsSnapshot struct {
	Drivers      int     `json:"drivers"`
	Available    int     `json:"available"`
	Reserved     int     `json:"reserved"`
	OnTrip       int     `json:"onTrip"`
	Requests     int     `json:"requests"`
	Matched      int     `json:"matched"`
	Unmatched    int     `json:"unmatched"`
	Offers       int     `json:"offers"`
	Accepted     int     `json:"accepted"`
	Declined     int     `json:"declined"`
	Expired      int     `json:"expired"`
	AcceptRate   float64 `json:"acceptRate"`
	AvgSearchMs  float64 `json:"avgSearchMs"`
	AvgSearchUs  float64 `json:"avgSearchUs"`
	P95SearchMs  float64 `json:"p95SearchMs"`
	AvgAssignMs  float64 `json:"avgAssignMs"`
	AvgCells     float64 `json:"avgCells"`
	AvgScanned   float64 `json:"avgDriversScanned"`
	AvgEtaSec    float64 `json:"avgEtaSec"`
	LiveSessions int     `json:"liveSessions"`
	Dropped      uint64  `json:"droppedEvents"`
}

func (m *metrics) snapshot() StatsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := StatsSnapshot{
		Requests:    m.requests,
		Matched:     m.matched,
		Unmatched:   m.unmatched,
		Offers:      m.offers,
		Accepted:    m.accepted,
		Declined:    m.declined,
		Expired:     m.expired,
		AvgSearchMs: round2(avg(m.searchMs)),
		AvgSearchUs: round1(avg(m.searchMs) * 1000),
		P95SearchMs: round2(percentile(m.searchMs, 0.95)),
		AvgAssignMs: round2(avg(m.assignMs)),
		AvgCells:    round2(avg(m.cells)),
		AvgScanned:  round2(avg(m.scanned)),
		AvgEtaSec:   round2(avg(m.etas)),
	}
	if m.offers > 0 {
		s.AcceptRate = round2(float64(m.accepted) / float64(m.offers))
	}
	return s
}

func (m *metrics) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Never assign *m = metrics{} here: that would overwrite the locked mutex
	// itself and the deferred Unlock would then operate on a fresh, unlocked
	// mutex (fatal error: sync: unlock of unlocked mutex).
	m.requests = 0
	m.matched = 0
	m.unmatched = 0
	m.offers = 0
	m.accepted = 0
	m.declined = 0
	m.expired = 0
	m.searchMs = nil
	m.assignMs = nil
	m.cells = nil
	m.scanned = nil
	m.etas = nil
}

func avg(s []float64) float64 {
	if len(s) == 0 {
		return 0
	}
	var sum float64
	for _, v := range s {
		sum += v
	}
	return sum / float64(len(s))
}

func percentile(s []float64, p float64) float64 {
	if len(s) == 0 {
		return 0
	}
	cp := make([]float64, len(s))
	copy(cp, s)
	sort.Float64s(cp)
	idx := int(float64(len(cp)-1) * p)
	return cp[idx]
}
