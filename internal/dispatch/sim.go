package dispatch

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"rahyab/internal/geo"
)

// Simulator keeps the demo alive: idle cars cruise around, accepted trips drive
// to their drop-off and complete, and — when enabled — synthetic rider demand
// is injected. Everything it does goes through the same Store methods that a
// production GPS ingestion path would use, so the matching code under test is
// exactly the production code.
type Simulator struct {
	store  *Store
	bus    *Bus
	engine *Engine
	rnd    *rand.Rand

	mu         sync.Mutex
	autoDemand bool

	stop chan struct{}
	once sync.Once
}

// NewSimulator builds a simulator with a fixed random seed.
func NewSimulator(store *Store, bus *Bus, engine *Engine) *Simulator {
	return &Simulator{
		store:  store,
		bus:    bus,
		engine: engine,
		rnd:    rand.New(rand.NewSource(9)),
		stop:   make(chan struct{}),
	}
}

// Start launches the simulation loop.
func (s *Simulator) Start() { go s.run() }

// Stop terminates the simulation loop.
func (s *Simulator) Stop() {
	s.once.Do(func() { close(s.stop) })
}

// SetAutoDemand toggles synthetic ride requests.
func (s *Simulator) SetAutoDemand(v bool) {
	s.mu.Lock()
	s.autoDemand = v
	s.mu.Unlock()
}

// AutoDemand reports the synthetic demand setting.
func (s *Simulator) AutoDemand() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.autoDemand
}

func (s *Simulator) run() {
	t := time.NewTicker(800 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.tick(0.8)
			if s.AutoDemand() && s.rnd.Float64() < 0.16 {
				s.injectDemand()
			}
		}
	}
}

func (s *Simulator) tick(dt float64) {
	drivers := s.store.Snapshot()

	for i := range drivers {
		d := &drivers[i]
		switch d.State {
		case StateOnTrip:
			if d.Trip == nil {
				continue
			}
			// A real ride takes minutes; the demo compresses it by ~12x so a
			// trip completes while the visitor is still looking at the map.
			step := (44.0 / 3.6) * dt * 12
			phase := d.Trip.Phase
			if phase == "" {
				phase = PhaseToPickup
			}
			target := d.Trip.Dropoff
			if phase == PhaseToPickup {
				target = d.Trip.Pickup
			}
			dist := geo.DistanceMeters(d.Pos, target)
			if dist <= step+10 {
				s.store.Move(d.ID, target, d.Heading)
				if phase == PhaseToPickup {
					if tr, ok := s.store.MarkPickedUp(d.ID); ok {
						s.bus.Publish("trip.picked_up", map[string]any{
							"tripId":   tr.ID,
							"driverId": tr.DriverID,
							"at":       time.Now(),
						})
					}
				} else {
					s.finishTrip(d.ID)
				}
				continue
			}
			bearing := geo.BearingDeg(d.Pos, target)
			s.store.Move(d.ID, geo.Destination(d.Pos, bearing, step), bearing)

		case StateAvailable:
			heading := math.Mod(d.Heading+(s.rnd.Float64()*2-1)*20+360, 360)
			step := ((28.0 + s.rnd.Float64()*20) / 3.6) * dt * 4
			next := geo.Destination(d.Pos, heading, step)
			if !insideBounds(next) {
				center := geo.Point{Lat: CenterLat, Lon: CenterLon}
				heading = geo.BearingDeg(d.Pos, center)
				next = geo.Destination(d.Pos, heading, step)
			}
			s.store.Move(d.ID, next, heading)

		case StateReserved:
			// Waiting for the rider's answer; keep the car where it is.
		}
	}

	s.publishFleet()
}

func (s *Simulator) finishTrip(driverID string) {
	drv, tr, ok := s.store.CompleteTrip(driverID)
	if !ok {
		return
	}
	s.bus.Publish("trip.finished", map[string]any{
		"tripId":      tr.ID,
		"requestId":   tr.RequestID,
		"driverId":    drv.ID,
		"driverName":  drv.Name,
		"fare":        tr.Fare,
		"distanceM":   round1(tr.DistanceM),
		"durationSec": round2(time.Since(tr.StartedAt).Seconds()),
	})
}

// fleetPosition is the compact wire format for live positions: one letter keys,
// because this payload is sent to every open dashboard once per tick.
type fleetPosition struct {
	ID      string  `json:"i"`
	Lat     float64 `json:"a"`
	Lon     float64 `json:"o"`
	Heading float64 `json:"h"`
	State   State   `json:"s"`
}

func (s *Simulator) publishFleet() {
	drivers := s.store.Snapshot()
	out := make([]fleetPosition, 0, len(drivers))
	for _, d := range drivers {
		out = append(out, fleetPosition{
			ID:      d.ID,
			Lat:     math.Round(d.Pos.Lat*1e6) / 1e6,
			Lon:     math.Round(d.Pos.Lon*1e6) / 1e6,
			Heading: math.Round(d.Heading*10) / 10,
			State:   d.State,
		})
	}
	s.bus.Publish("fleet", out)
}

func (s *Simulator) injectDemand() {
	latSpan := CityBounds.MaxLat - CityBounds.MinLat
	lonSpan := CityBounds.MaxLon - CityBounds.MinLon

	var pickup, drop geo.Point
	for attempt := 0; attempt < 6; attempt++ {
		pickup = geo.Point{
			Lat: CityBounds.MinLat + 0.07*latSpan + s.rnd.Float64()*0.86*latSpan,
			Lon: CityBounds.MinLon + 0.07*lonSpan + s.rnd.Float64()*0.86*lonSpan,
		}
		drop = geo.Point{
			Lat: CityBounds.MinLat + 0.05*latSpan + s.rnd.Float64()*0.9*latSpan,
			Lon: CityBounds.MinLon + 0.05*lonSpan + s.rnd.Float64()*0.9*lonSpan,
		}
		if geo.DistanceMeters(pickup, drop) > 1800 {
			break
		}
	}
	if _, err := s.engine.Submit(pickup, drop); err != nil {
		return
	}
	s.bus.Publish("demand.injected", map[string]any{"pickup": pickup, "dropoff": drop})
}

func insideBounds(p geo.Point) bool {
	return p.Lat >= CityBounds.MinLat && p.Lat <= CityBounds.MaxLat &&
		p.Lon >= CityBounds.MinLon && p.Lon <= CityBounds.MaxLon
}
