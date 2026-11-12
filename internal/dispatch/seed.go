package dispatch

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"rahyab/internal/geo"
)

var firstNames = []string{
	"علی", "رضا", "مهدی", "حسین", "امیر", "سعید", "محمد", "فرهاد", "بهنام", "کیوان",
	"سینا", "پویا", "نیما", "آرش", "بابک", "کامران", "شهاب", "یاسر", "مجید", "وحید",
	"حامد", "پیمان", "امید", "نوید", "مسعود", "ابراهیم", "جواد", "حمید", "رامین", "سامان",
}

var lastNames = []string{
	"احمدی", "رضایی", "کریمی", "موسوی", "حسینی", "صادقی", "نیکنام", "شریفی", "قاسمی", "بهرامی",
	"سلطانی", "امینی", "طاهری", "فرهادی", "کاظمی", "مرادی", "یزدانی", "خسروی", "بابایی", "نوری",
}

var carModels = []string{
	"سمند LX", "پژو ۲۰۶", "پژو پارس", "تیبا ۲", "ساینا S", "شاهین", "دنا پلاس", "کوییک R", "اچ‌سی کراس", "رانا پلاس",
}

var carColors = []string{"سفید", "نقره‌ای", "مشکی", "خاکستری", "آبی", "سرمه‌ای"}

var plateLetters = []string{"ب", "ج", "د", "س", "ص", "ط", "ق", "ل", "م", "ن", "و", "ه", "ی"}
var plateProvinces = []int{11, 22, 33, 44, 55, 68, 78, 95}

// SeedDrivers builds a deterministic demo fleet. The generator is seeded with a
// constant so that a page reload shows the same city, which makes it possible
// to compare two matching strategies on identical supply.
func SeedDrivers(n int) []Driver {
	if n <= 0 {
		return nil
	}
	rnd := rand.New(rand.NewSource(20261112))
	now := time.Now()

	latSpan := CityBounds.MaxLat - CityBounds.MinLat
	lonSpan := CityBounds.MaxLon - CityBounds.MinLon

	out := make([]Driver, 0, n)
	for i := 0; i < n; i++ {
		lat := CityBounds.MinLat + 0.02*latSpan + rnd.Float64()*0.96*latSpan
		lon := CityBounds.MinLon + 0.02*lonSpan + rnd.Float64()*0.96*lonSpan

		name := firstNames[rnd.Intn(len(firstNames))] + " " + lastNames[rnd.Intn(len(lastNames))]
		car := carModels[rnd.Intn(len(carModels))] + " " + carColors[rnd.Intn(len(carColors))]
		plate := fmt.Sprintf("%02d %s %03d · ایران %02d",
			10+rnd.Intn(89),
			plateLetters[rnd.Intn(len(plateLetters))],
			100+rnd.Intn(899),
			plateProvinces[rnd.Intn(len(plateProvinces))],
		)

		rating := math.Round((4.25+rnd.Float64()*0.73)*100) / 100
		acceptance := math.Round((0.58+rnd.Float64()*0.41)*100) / 100

		out = append(out, Driver{
			ID:         fmt.Sprintf("drv_%03d", i+1),
			Name:       name,
			Car:        car,
			Plate:      plate,
			Rating:     rating,
			Trips:      90 + rnd.Intn(8500),
			Acceptance: acceptance,
			Pos:        geo.Point{Lat: lat, Lon: lon},
			Heading:    rnd.Float64() * 360,
			State:      StateAvailable,
			IdleSince:  now.Add(-time.Duration(rnd.Intn(900)) * time.Second),
		})
	}
	return out
}

// SeedRequests generates a burst of plausible ride requests for the benchmark
// harness and for demos that want instant load.
func SeedRequests(n int, rnd *rand.Rand) [][2]geo.Point {
	out := make([][2]geo.Point, 0, n)
	latSpan := CityBounds.MaxLat - CityBounds.MinLat
	lonSpan := CityBounds.MaxLon - CityBounds.MinLon
	for i := 0; i < n; i++ {
		pickup := geo.Point{
			Lat: CityBounds.MinLat + 0.08*latSpan + rnd.Float64()*0.84*latSpan,
			Lon: CityBounds.MinLon + 0.08*lonSpan + rnd.Float64()*0.84*lonSpan,
		}
		var drop geo.Point
		for attempt := 0; attempt < 8; attempt++ {
			drop = geo.Point{
				Lat: CityBounds.MinLat + 0.05*latSpan + rnd.Float64()*0.9*latSpan,
				Lon: CityBounds.MinLon + 0.05*lonSpan + rnd.Float64()*0.9*lonSpan,
			}
			if geo.DistanceMeters(pickup, drop) > 1500 {
				break
			}
		}
		out = append(out, [2]geo.Point{pickup, drop})
	}
	return out
}
