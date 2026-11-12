// Package geo contains the small amount of geometry the dispatcher needs:
// great-circle distance, bearings and a local metric projection that lets us
// reason about the city as a flat plane of metres.
package geo

import "math"

// earthRadiusM is the mean earth radius in metres (IUGG).
const earthRadiusM = 6371008.8

// Point is a WGS84 coordinate.
type Point struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// IsZero reports whether the point is unset.
func (p Point) IsZero() bool { return p.Lat == 0 && p.Lon == 0 }

func rad(d float64) float64 { return d * math.Pi / 180 }
func deg(r float64) float64 { return r * 180 / math.Pi }

// DistanceMeters returns the great-circle distance between a and b.
func DistanceMeters(a, b Point) float64 {
	lat1, lat2 := rad(a.Lat), rad(b.Lat)
	dLat := lat2 - lat1
	dLon := rad(b.Lon - a.Lon)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	if h > 1 {
		h = 1
	}
	return 2 * earthRadiusM * math.Asin(math.Sqrt(h))
}

// BearingDeg returns the initial bearing from a to b in degrees, [0,360).
func BearingDeg(a, b Point) float64 {
	lat1, lat2 := rad(a.Lat), rad(b.Lat)
	dLon := rad(b.Lon - a.Lon)
	y := math.Sin(dLon) * math.Cos(lat2)
	x := math.Cos(lat1)*math.Sin(lat2) - math.Sin(lat1)*math.Cos(lat2)*math.Cos(dLon)
	return math.Mod(deg(math.Atan2(y, x))+360, 360)
}

// AngleDiff returns the smallest absolute difference between two bearings,
// in degrees within [0,180].
func AngleDiff(a, b float64) float64 {
	d := math.Mod(math.Abs(a-b), 360)
	if d > 180 {
		d = 360 - d
	}
	return d
}

// Destination returns the point reached when travelling distanceM metres from
// p along the given bearing. Used by the fleet simulator to move cars.
func Destination(p Point, bearingDeg, distanceM float64) Point {
	delta := distanceM / earthRadiusM
	theta := rad(bearingDeg)
	lat1 := rad(p.Lat)
	lon1 := rad(p.Lon)

	sinLat2 := math.Sin(lat1)*math.Cos(delta) + math.Cos(lat1)*math.Sin(delta)*math.Cos(theta)
	lat2 := math.Asin(math.Min(1, math.Max(-1, sinLat2)))
	lon2 := lon1 + math.Atan2(
		math.Sin(theta)*math.Sin(delta)*math.Cos(lat1),
		math.Cos(delta)-math.Sin(lat1)*sinLat2,
	)
	return Point{Lat: deg(lat2), Lon: deg(lon2)}
}

// Projector maps WGS84 coordinates onto a local tangent plane whose origin is
// the given city centre. Within a single city the error of this equirectangular
// approximation stays below a few centimetres per kilometre, which is far below
// GPS noise and therefore invisible to the matching algorithm.
type Projector struct {
	lat0   float64
	lon0   float64
	cosLat float64
}

// NewProjector builds a projection centred on origin.
func NewProjector(origin Point) Projector {
	return Projector{lat0: origin.Lat, lon0: origin.Lon, cosLat: math.Cos(rad(origin.Lat))}
}

// XY converts a coordinate to metres east/north of the projection origin.
func (p Projector) XY(pt Point) (x, y float64) {
	const metersPerDegree = earthRadiusM * math.Pi / 180
	x = (pt.Lon - p.lon0) * metersPerDegree * p.cosLat
	y = (pt.Lat - p.lat0) * metersPerDegree
	return x, y
}

// Point converts metres east/north back to a coordinate.
func (p Projector) Point(x, y float64) Point {
	const metersPerDegree = earthRadiusM * math.Pi / 180
	return Point{
		Lat: p.lat0 + deg(y/earthRadiusM),
		Lon: p.lon0 + deg(x/(earthRadiusM*p.cosLat)),
	}
}

// DistXY returns the euclidean distance between two projected points.
func DistXY(ax, ay, bx, by float64) float64 {
	return math.Hypot(ax-bx, ay-by)
}
