package main

import (
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"mf-mvp/handler"
	"mf-mvp/service"
)

func main() {
	// Load NAV data from mfapi.in (blocks until all schemes are fetched).
	svc, err := service.NewFundService()
	if err != nil {
		log.Fatalf("failed to initialise fund service: %v", err)
	}

	cache := handler.NewResponseCache()
	h := handler.NewFundHandler(svc, cache)

	// Run one sync immediately after startup (non-blocking).
	go func() {
		log.Println("[sync] startup sync triggered")
		svc.TriggerSync()
	}()

	// Background scheduler: incremental sync every 6 hours, flush cache on success.
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			log.Println("[sync] scheduled sync triggered")
			if started := svc.TriggerSync(); !started {
				log.Println("[sync] skipped — already in progress")
				continue
			}
			// Wait for the sync to finish before flushing cache.
			// TriggerSync is non-blocking; poll status until no longer running.
			for {
				status, _, _, _ := svc.GetSyncStatus()
				if status != "running" {
					break
				}
				time.Sleep(5 * time.Second)
			}
			cache.Flush()
			log.Println("[refresh] cache flushed after scheduled sync")
		}
	}()

	r := mux.NewRouter()

	// More-specific routes must come before /{code} to avoid gorilla/mux
	// treating path segments like "rank" or "analytics" as a {code} variable.
	r.HandleFunc("/funds/rank", h.RankFunds).Methods(http.MethodGet)
	r.HandleFunc("/funds/{code}/analytics", h.GetAnalytics).Methods(http.MethodGet)
	r.HandleFunc("/funds/{code}", h.GetFund).Methods(http.MethodGet)
	r.HandleFunc("/funds", h.ListFunds).Methods(http.MethodGet)
	r.HandleFunc("/sync/trigger", h.TriggerSync).Methods(http.MethodPost)
	r.HandleFunc("/sync/status", h.GetSyncStatus).Methods(http.MethodGet)

	addr := ":8080"
	log.Printf("server listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}