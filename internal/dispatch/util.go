package dispatch

import (
	"sync/atomic"
	"time"
)

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

var idCounter atomic.Uint64

var idPrefixes = map[byte]string{
	'r': "req",
	'o': "off",
	't': "trip",
}

// nextID returns a short, monotonic, sortable id. It is deliberately not a
// UUID: ids appear in logs and dashboards, and a 12 character counter is easier
// to follow than 36 hex characters.
func nextID(kind byte) string {
	n := idCounter.Add(1)
	prefix := idPrefixes[kind]
	buf := make([]byte, 0, len(prefix)+1+12)
	buf = append(buf, prefix...)
	buf = append(buf, '_')
	buf = appendInt(buf, n)
	return string(buf)
}

func appendInt(b []byte, n uint64) []byte {
	if n == 0 {
		return append(b, '0')
	}
	var tmp [20]byte
	i := len(tmp)
	for n > 0 {
		i--
		tmp[i] = byte('0' + n%10)
		n /= 10
	}
	return append(b, tmp[i:]...)
}

// elapsedMs converts a duration to milliseconds with microsecond resolution.
func elapsedMs(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}
