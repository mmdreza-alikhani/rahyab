// Package api exposes the dispatcher over HTTP and streams live state to the
// dashboard. It is intentionally thin: all decisions live in the dispatch
// package, handlers only translate JSON.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"rahyab/internal/dispatch"
	"rahyab/internal/geo"
	"rahyab/web"
)

// Server owns the HTTP surface.
type Server struct {
	engine    *dispatch.Engine
	store     *dispatch.Store
	bus       *dispatch.Bus
	sim       *dispatch.Simulator
	seedCount int
}

// New builds the HTTP server facade.
func New(engine *dispatch.Engine, store *dispatch.Store, bus *dispatch.Bus, sim *dispatch.Simulator, seedCount int) *Server {
	if seedCount <= 0 {
		seedCount = 72
	}
	return &Server{engine: engine, store: store, bus: bus, sim: sim, seedCount: seedCount}
}

// Handler returns the routed http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("POST /api/request", s.handleRequest)
	mux.HandleFunc("POST /api/offers/{id}/accept", s.handleAccept)
	mux.HandleFunc("POST /api/offers/{id}/decline", s.handleDecline)
	mux.HandleFunc("POST /api/settings", s.handleSettings)
	mux.HandleFunc("POST /api/reset", s.handleReset)
	mux.HandleFunc("GET /api/events", s.handleEvents)

	files := http.FileServer(http.FS(web.FS))
	mux.Handle("GET /", noStore(files))
	return mux
}

type cityInfo struct {
	Center geo.Point `json:"center"`
	MinLat float64   `json:"minLat"`
	MaxLat float64   `json:"maxLat"`
	MinLon float64   `json:"minLon"`
	MaxLon float64   `json:"maxLon"`
}

type settings struct {
	AutoAccept bool `json:"autoAccept"`
	AutoDemand bool `json:"autoDemand"`
}

type stateResponse struct {
	City       cityInfo               `json:"city"`
	Drivers    []dispatch.Driver      `json:"drivers"`
	Config     dispatch.Config        `json:"config"`
	Stats      dispatch.StatsSnapshot `json:"stats"`
	Settings   settings               `json:"settings"`
	ServerTime time.Time              `json:"serverTime"`
}

func (s *Server) currentSettings() settings {
	return settings{
		AutoAccept: s.engine.AutoAccept(),
		AutoDemand: s.sim.AutoDemand(),
	}
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	resp := stateResponse{
		City: cityInfo{
			Center: geo.Point{Lat: dispatch.CenterLat, Lon: dispatch.CenterLon},
			MinLat: dispatch.CityBounds.MinLat,
			MaxLat: dispatch.CityBounds.MaxLat,
			MinLon: dispatch.CityBounds.MinLon,
			MaxLon: dispatch.CityBounds.MaxLon,
		},
		Drivers:    s.engine.Fleet(),
		Config:     s.engine.ConfigSnapshot(),
		Stats:      s.engine.Stats(),
		Settings:   s.currentSettings(),
		ServerTime: time.Now(),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Stats())
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "time": time.Now()})
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pickup  geo.Point `json:"pickup"`
		Dropoff geo.Point `json:"dropoff"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "بدنه درخواست معتبر نیست")
		return
	}
	id, err := s.engine.Submit(body.Pickup, body.Dropoff)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"requestId": id})
}

func (s *Server) handleAccept(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	trip, err := s.engine.Accept(id)
	if err != nil {
		switch {
		case errors.Is(err, dispatch.ErrOfferNotFound):
			writeErr(w, http.StatusNotFound, "این پیشنهاد دیگر فعال نیست")
		case errors.Is(err, dispatch.ErrOfferExpired):
			writeErr(w, http.StatusGone, "زمان پاسخ به پیشنهاد تمام شده است")
		case errors.Is(err, dispatch.ErrDriverBusy):
			writeErr(w, http.StatusConflict, "راننده در وضعیت دیگری است")
		default:
			writeErr(w, http.StatusInternalServerError, "خطای داخلی توزیع")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trip": trip})
}

func (s *Server) handleDecline(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.engine.Decline(id); err != nil {
		if errors.Is(err, dispatch.ErrOfferNotFound) {
			writeErr(w, http.StatusNotFound, "این پیشنهاد دیگر فعال نیست")
			return
		}
		writeErr(w, http.StatusInternalServerError, "خطای داخلی توزیع")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AutoAccept *bool `json:"autoAccept"`
		AutoDemand *bool `json:"autoDemand"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "بدنه درخواست معتبر نیست")
		return
	}
	if body.AutoAccept != nil {
		s.engine.SetAutoAccept(*body.AutoAccept)
	}
	if body.AutoDemand != nil {
		s.sim.SetAutoDemand(*body.AutoDemand)
	}
	writeJSON(w, http.StatusOK, s.currentSettings())
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	n := s.seedCount
	if v := r.URL.Query().Get("drivers"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 500 {
			n = parsed
		}
	}
	s.engine.Reset(dispatch.SeedDrivers(n))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "drivers": n})
}

// handleEvents streams every dispatch event as server-sent events. SSE is used
// instead of WebSockets on purpose: the traffic is one-way, the browser
// reconnects automatically, and the server needs no extra dependency.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 2000\n\n")
	flusher.Flush()

	ch, cancel := s.bus.Subscribe()
	defer cancel()

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			payload, err := json.Marshal(ev.Data)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("api: encode: %v", err)
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func noStore(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		h.ServeHTTP(w, r)
	})
}
