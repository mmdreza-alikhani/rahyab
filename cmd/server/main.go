// Command server runs the Rahyab dispatch demo: a Go matching engine, a
// simulated fleet of drivers and the embedded operator dashboard.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"rahyab/internal/api"
	"rahyab/internal/dispatch"
	"rahyab/internal/geo"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	driverCount := flag.Int("drivers", 72, "number of simulated drivers")
	cellMeters := flag.Float64("cell", 450, "spatial grid cell size in metres")
	batchMs := flag.Int("batch", 260, "matching batch window in milliseconds")
	flag.Parse()

	proj := geo.NewProjector(geo.Point{Lat: dispatch.CenterLat, Lon: dispatch.CenterLon})
	store := dispatch.NewStore(proj, *cellMeters)
	store.Reset(dispatch.SeedDrivers(*driverCount))

	router := geo.DefaultRoadModel()
	bus := dispatch.NewBus()

	cfg := dispatch.DefaultConfig()
	cfg.CellMeters = *cellMeters
	cfg.BatchMs = *batchMs

	engine := dispatch.NewEngine(cfg, store, router, bus, proj)
	engine.SetAutoAccept(true)
	engine.Start()

	sim := dispatch.NewSimulator(store, bus, engine)
	sim.Start()
	sim.SetAutoDemand(false)

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           api.New(engine, store, bus, sim, *driverCount).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      0, // SSE connections are long-lived
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("rahyab: %d drivers, %.0f m cells, batch %d ms", *driverCount, *cellMeters, *batchMs)
		log.Printf("rahyab: dashboard on http://%s", *addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		log.Fatalf("rahyab: %v", err)
	case <-stop:
		log.Printf("rahyab: shutting down")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	engine.Stop()
	sim.Stop()
}
