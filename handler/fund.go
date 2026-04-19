package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"mf-mvp/service"
)

// FundHandler wires HTTP requests to the FundService.
type FundHandler struct {
	svc   *service.FundService
	cache *ResponseCache
}

// NewFundHandler creates a FundHandler with an attached response cache.
func NewFundHandler(svc *service.FundService, cache *ResponseCache) *FundHandler {
	return &FundHandler{svc: svc, cache: cache}
}

// ─── Response shapes ──────────────────────────────────────────────────────────

type fundSummary struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	LatestNAV float64 `json:"latest_nav"`
}

type fundDetail struct {
	Code      string      `json:"code"`
	Name      string      `json:"name"`
	LatestNAV float64     `json:"latest_nav"`
	NAVs      interface{} `json:"navs"`
}

type rankEntry struct {
	Rank         int     `json:"rank"`
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	LatestNAV    float64 `json:"latest_nav"`
	OldestNAV    float64 `json:"oldest_nav"`
	SimpleReturn float64 `json:"simple_return_pct"`
}

type analyticsRankEntry struct {
	Rank          int     `json:"rank"`
	FundCode      string  `json:"fund_code"`
	FundName      string  `json:"fund_name"`
	MedianReturn  float64 `json:"median_return"`
	MaxDrawdown   float64 `json:"max_drawdown"`
	CurrentNAV    float64 `json:"current_nav"`
	LastUpdated   string  `json:"last_updated"`
}

type analyticsRankResponse struct {
	Category   string               `json:"category"`
	Window     string               `json:"window"`
	SortedBy   string               `json:"sorted_by"`
	TotalFunds int                  `json:"total_funds"`
	Showing    int                  `json:"showing"`
	Funds      []analyticsRankEntry `json:"funds"`
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

// ListFunds handles GET /funds
func (h *FundHandler) ListFunds(w http.ResponseWriter, r *http.Request) {
	const key = "funds"

	if data, ok := h.cache.Get(key); ok {
		log.Println("[cache] hit: /funds")
		writeBytes(w, data)
		return
	}
	log.Println("[cache] miss: /funds")

	funds := h.svc.ListFunds()
	resp := make([]fundSummary, 0, len(funds))
	for _, f := range funds {
		resp = append(resp, fundSummary{
			Code:      f.Code,
			Name:      f.Name,
			LatestNAV: f.LatestNAV(),
		})
	}

	data, _ := json.Marshal(resp)
	h.cache.Set(key, data)
	writeBytes(w, data)
}

// GetFund handles GET /funds/{code}
func (h *FundHandler) GetFund(w http.ResponseWriter, r *http.Request) {
	code := mux.Vars(r)["code"]
	key := "fund:" + code

	if data, ok := h.cache.Get(key); ok {
		log.Printf("[cache] hit: /funds/%s", code)
		writeBytes(w, data)
		return
	}
	log.Printf("[cache] miss: /funds/%s", code)

	fund, ok := h.svc.GetFund(code)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "fund not found: " + code,
		})
		return
	}

	resp := fundDetail{
		Code:      fund.Code,
		Name:      fund.Name,
		LatestNAV: fund.LatestNAV(),
		NAVs:      fund.NAVs,
	}
	data, _ := json.Marshal(resp)
	h.cache.Set(key, data)
	writeBytes(w, data)
}

// RankFunds handles GET /funds/rank
//
// Without query params → legacy simple-return ranking (unchanged behaviour).
// With ?category=&window= → analytics-based ranking with optional sort_by and limit.
func (h *FundHandler) RankFunds(w http.ResponseWriter, r *http.Request) {
	key := "rank:" + r.URL.RawQuery

	if data, ok := h.cache.Get(key); ok {
		log.Printf("[cache] hit: /funds/rank?%s", r.URL.RawQuery)
		writeBytes(w, data)
		return
	}
	log.Printf("[cache] miss: /funds/rank?%s", r.URL.RawQuery)

	q := r.URL.Query()
	category := q.Get("category")
	window := q.Get("window")

	var (
		resp interface{}
		code = http.StatusOK
	)

	// Legacy path: no analytics params supplied.
	if category == "" && window == "" {
		funds := h.svc.RankedFunds()
		entries := make([]rankEntry, 0, len(funds))
		for i, f := range funds {
			entries = append(entries, rankEntry{
				Rank:         i + 1,
				Code:         f.Code,
				Name:         f.Name,
				LatestNAV:    f.LatestNAV(),
				OldestNAV:    f.OldestNAV(),
				SimpleReturn: f.SimpleReturn(),
			})
		}
		resp = entries

	} else {
		// Analytics path: both category and window are required.
		if category == "" || window == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "both 'category' and 'window' query params are required for analytics ranking",
			})
			return
		}

		sortBy := q.Get("sort_by")
		if sortBy == "" {
			sortBy = "median_return"
		}
		if sortBy != "median_return" && sortBy != "max_drawdown" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "sort_by must be 'median_return' or 'max_drawdown'",
			})
			return
		}

		limit := 5
		if lStr := q.Get("limit"); lStr != "" {
			if _, err := fmt.Sscanf(lStr, "%d", &limit); err != nil || limit < 1 {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": "limit must be a positive integer",
				})
				return
			}
		}

		result, err := h.svc.RankedByAnalytics(category, window, sortBy, limit)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		entries := make([]analyticsRankEntry, len(result.Funds))
		for i, f := range result.Funds {
			entries[i] = analyticsRankEntry{
				Rank:         i + 1,
				FundCode:     f.Code,
				FundName:     f.Name,
				MedianReturn: f.MedianReturn,
				MaxDrawdown:  f.MaxDrawdown,
				CurrentNAV:   f.CurrentNAV,
				LastUpdated:  f.LastUpdated,
			}
		}

		resp = analyticsRankResponse{
			Category:   category,
			Window:     window,
			SortedBy:   sortBy,
			TotalFunds: result.TotalFunds,
			Showing:    len(entries),
			Funds:      entries,
		}
	}

	data, _ := json.Marshal(resp)
	h.cache.Set(key, data)
	writeBytes(w, data)
	_ = code // status is always 200 on the success path above
}

// GetAnalytics handles GET /funds/{code}/analytics?window=1Y|3Y|5Y|10Y
func (h *FundHandler) GetAnalytics(w http.ResponseWriter, r *http.Request) {
	code := mux.Vars(r)["code"]

	fund, ok := h.svc.GetFund(code)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "fund not found: " + code,
		})
		return
	}

	window := r.URL.Query().Get("window")
	if window == "" {
		window = "1Y" // sensible default
	}

	result, err := service.Analyze(fund, window)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
		})
		return
	}

	// Enrich fields that Analyze() cannot compute on its own.
	result.Category = service.FundCategory(code)
	result.AMC = ""        // not stored in model.Fund; extend Fund.AMC if needed later
	result.ComputedAt = time.Now().UTC().Format(time.RFC3339)

	writeJSON(w, http.StatusOK, result)
}

// TriggerSync handles POST /sync/trigger
func (h *FundHandler) TriggerSync(w http.ResponseWriter, r *http.Request) {
	started := h.svc.TriggerSync()
	if !started {
		writeJSON(w, http.StatusConflict, map[string]string{
			"message": "already in progress",
		})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"message": "sync started",
	})
}

// GetSyncStatus handles GET /sync/status
func (h *FundHandler) GetSyncStatus(w http.ResponseWriter, r *http.Request) {
	status, errMsg, lastRun, lastSuccess := h.svc.GetSyncStatus()

	resp := map[string]interface{}{
		"status":       status,
		"last_run":     nullableTime(lastRun),
		"last_success": nullableTime(lastSuccess),
		"error":        errMsg,
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeBytes serves pre-encoded JSON bytes (used by cached responses).
func writeBytes(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// nullableTime returns an RFC3339 string for non-zero times, or nil for zero
// values — so unset timestamps appear as JSON null rather than the zero date.
func nullableTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}