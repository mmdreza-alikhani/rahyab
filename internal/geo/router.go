package geo

import (
	"math"
	"time"
)

// Router estimates travel time between two points.
//
// The interface exists so that the heuristic road model below can be replaced
// by a real routing engine (OSRM / Valhalla / GraphHopper) without touching the
// dispatcher: only ETA quality changes, the matching pipeline stays identical.
type Router interface {
	// ETA returns the expected driving time in seconds.
	ETA(from, to Point, at time.Time) float64
	// Distance returns the expected driving distance in metres.
	Distance(from, to Point) float64
}

// RoadModel is a traffic-aware free-flow model.
//
// expected distance = great-circle distance * Detour
// expected time     = expected distance / (free-flow speed * traffic factor)
//
// Detour compensates for the fact that streets are not straight lines: in a
// dense grid city the ratio between driven and crow-fly distance sits around
// 1.3-1.4, which is what makes a naive "nearest by crow flies" ranking wrong
// often enough to matter.
type RoadModel struct {
	FreeFlowKmh float64
	Detour      float64
	// HandoffSec covers stopping, waiting for the passenger and pulling away.
	// It is a constant for a single request, but keeping it explicit makes the
	// ETA numbers agree with what riders actually experience.
	HandoffSec float64
}

// DefaultRoadModel is tuned for dense urban traffic.
func DefaultRoadModel() RoadModel {
	return RoadModel{FreeFlowKmh: 42, Detour: 1.34, HandoffSec: 35}
}

// Distance returns the modelled driving distance in metres.
func (m RoadModel) Distance(from, to Point) float64 {
	return DistanceMeters(from, to) * m.Detour
}

// ETA returns the modelled driving time in seconds.
func (m RoadModel) ETA(from, to Point, at time.Time) float64 {
	// TrafficFactor is a delay multiplier (>1 means slower than free flow),
	// so it divides the free-flow speed rather than multiplying it.
	kmh := m.FreeFlowKmh / TrafficFactor(at)
	if kmh < 4 {
		kmh = 4
	}
	return m.Distance(from, to)/(kmh*1000/3600) + m.HandoffSec
}

// TrafficFactor is a smooth daily congestion curve: two peaks (morning commute
// and evening rush) plus a mild midday plateau. Using gaussians instead of a
// step table avoids discontinuities that would make ETA rankings jump around
// at e.g. 10:00 sharp.
func TrafficFactor(at time.Time) float64 {
	h := float64(at.Hour()) + float64(at.Minute())/60
	f := 0.86
	f += 0.55 * gauss(h, 8.5, 1.15)
	f += 0.72 * gauss(h, 18.0, 1.35)
	f += 0.16 * gauss(h, 13.0, 2.6)
	if at.Weekday() == time.Friday {
		// Weekend traffic in Tehran is shifted later and lighter.
		f = 0.82 + 0.34*gauss(h, 19.5, 2.4) + 0.10*gauss(h, 12.0, 2.0)
	}
	if f < 0.7 {
		f = 0.7
	}
	return f
}

func gauss(x, mu, sigma float64) float64 {
	d := x - mu
	return math.Exp(-(d * d) / (2 * sigma * sigma))
}

// Compile-time assertion that RoadModel satisfies Router.
var _ Router = RoadModel{}
