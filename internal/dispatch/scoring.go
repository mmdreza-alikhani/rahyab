package dispatch

import (
	"math"
	"time"

	"rahyab/internal/geo"
)

// Weights turns the soft preferences of the marketplace into seconds, so that
// every term of the cost function has the same unit and the total stays
// explainable: "this driver costs 42 s more because he is heading the wrong way
// and has a low acceptance rate".
type Weights struct {
	Acceptance float64 `json:"acceptance"` // penalty at acceptance = 0
	Rating     float64 `json:"rating"`     // penalty per rating point below 4.9
	Fairness   float64 `json:"fairness"`   // maximum credit for a long-idle driver
	Heading    float64 `json:"heading"`    // penalty for driving away from pickup
}

// DefaultWeights is the production tuning used by the demo.
func DefaultWeights() Weights {
	return Weights{Acceptance: 190, Rating: 120, Fairness: 90, Heading: 65}
}

// ScoreBreakdown keeps the individual terms visible for the UI and for
// debugging an assignment after the fact.
type ScoreBreakdown struct {
	Eta        float64 `json:"eta"`
	Acceptance float64 `json:"acceptance"`
	Rating     float64 `json:"rating"`
	Fairness   float64 `json:"fairness"`
	Heading    float64 `json:"heading"`
}

// Candidate is a scored (driver, request) pair.
type Candidate struct {
	DriverID  string         `json:"driverId"`
	Name      string         `json:"name"`
	Car       string         `json:"car"`
	Plate     string         `json:"plate"`
	Rating    float64        `json:"rating"`
	Trips     int            `json:"trips"`
	DistanceM float64        `json:"distanceM"`
	EtaSec    float64        `json:"etaSec"`
	Cost      float64        `json:"cost"`
	Breakdown ScoreBreakdown `json:"breakdown"`
}

// scoreDriver builds the cost of sending d to pickup.
//
//	cost = eta
//	     + (1 - acceptance) * W.Acceptance
//	     + max(0, 4.9 - rating) * W.Rating
//	     + headingPenalty
//	     - idleCredit
//
// Only the ETA term is a hard physical quantity; the rest are marketplace
// preferences expressed in equivalent seconds of waiting. Summing them over a
// batch is what makes the assignment globally optimal instead of greedy.
func scoreDriver(d Driver, pickup geo.Point, now time.Time, r geo.Router, w Weights) Candidate {
	dist := r.Distance(d.Pos, pickup)
	eta := r.ETA(d.Pos, pickup, now)

	acceptancePenalty := (1 - clamp01(d.Acceptance)) * w.Acceptance
	ratingPenalty := math.Max(0, 4.9-d.Rating) * w.Rating

	diff := geo.AngleDiff(d.Heading, geo.BearingDeg(d.Pos, pickup))
	headingPenalty := 0.0
	if diff > 45 {
		headingPenalty = math.Min(1, (diff-45)/135) * w.Heading
	}

	idle := math.Min(d.IdleFor(now), 900) / 900
	idleCredit := idle * w.Fairness

	cost := eta + acceptancePenalty + ratingPenalty + headingPenalty - idleCredit
	if cost < 30 {
		cost = 30
	}

	return Candidate{
		DriverID:  d.ID,
		Name:      d.Name,
		Car:       d.Car,
		Plate:     d.Plate,
		Rating:    d.Rating,
		Trips:     d.Trips,
		DistanceM: dist,
		EtaSec:    eta,
		Cost:      cost,
		Breakdown: ScoreBreakdown{
			Eta:        round1(eta),
			Acceptance: round1(acceptancePenalty),
			Rating:     round1(ratingPenalty),
			Fairness:   round1(-idleCredit),
			Heading:    round1(headingPenalty),
		},
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
