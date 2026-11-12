package dispatch

import (
	"time"

	"rahyab/internal/geo"
)

// State is the driver lifecycle state. The dispatcher only ever assigns a
// driver that is Available, and it reserves that driver atomically before an
// offer is created so two riders can never be offered the same car.
type State string

const (
	StateAvailable State = "available"
	StateReserved  State = "reserved"
	StateOnTrip    State = "on_trip"
)

// Trip phases. A trip has two legs: reach the rider, then drive to the
// destination. The phase is stored explicitly because deriving it from the
// distance to the pickup makes the car oscillate: after moving one step toward
// the drop-off it is again more than 45 m away from the pickup, so a distance
// test alone would send it back.
const (
	PhaseToPickup  = "to_pickup"
	PhaseToDropoff = "to_dropoff"
)

// Trip is a confirmed ride.
type Trip struct {
	ID        string    `json:"id"`
	RequestID string    `json:"requestId"`
	DriverID  string    `json:"driverId"`
	Pickup    geo.Point `json:"pickup"`
	Dropoff   geo.Point `json:"dropoff"`
	Phase     string    `json:"phase"`
	StartedAt time.Time `json:"startedAt"`
	DistanceM float64   `json:"distanceM"`
	Fare      int       `json:"fare"`
}

// Driver is the unit of supply.
type Driver struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Car        string    `json:"car"`
	Plate      string    `json:"plate"`
	Rating     float64   `json:"rating"`
	Trips      int       `json:"trips"`
	Acceptance float64   `json:"acceptance"` // share of offers accepted, 0..1
	Pos        geo.Point `json:"pos"`
	Heading    float64   `json:"heading"`
	State      State     `json:"state"`
	OfferID    string    `json:"offerId,omitempty"`
	Trip       *Trip     `json:"trip,omitempty"`
	IdleSince  time.Time `json:"-"`
}

// IdleFor returns how long the driver has been waiting for work.
func (d Driver) IdleFor(now time.Time) float64 {
	if d.IdleSince.IsZero() {
		return 0
	}
	v := now.Sub(d.IdleSince).Seconds()
	if v < 0 {
		return 0
	}
	return v
}

// City bounds used by seeding, simulation and by the API for the map viewport.
const (
	CenterLat = 35.7219
	CenterLon = 51.4215
)

// CityBounds describes the demo operating area (central Tehran).
var CityBounds = struct {
	MinLat, MaxLat, MinLon, MaxLon float64
}{
	MinLat: 35.6750,
	MaxLat: 35.7690,
	MinLon: 51.3350,
	MaxLon: 51.4900,
}
