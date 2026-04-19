package service

import (
	"fmt"
	"math"
	"sort"
	"time"

	"mf-mvp/model"
)

// windowDays maps the API query param to trading-day counts.
var windowDays = map[string]int{
	"1Y":  252,
	"3Y":  756,
	"5Y":  1260,
	"10Y": 2520,
}

// Analyze computes rolling returns, max drawdown, and CAGR for a fund
// over the requested window label ("1Y", "3Y", "5Y", "10Y").
//
// NAV data is expected latest-first (index 0 = most recent); it is
// reversed internally to oldest-first before any calculation.
//
// Category, AMC, and ComputedAt are filled by the handler after this call.
func Analyze(fund *model.Fund, windowLabel string) (*model.AnalyticsResult, error) {
	days, ok := windowDays[windowLabel]
	if !ok {
		return nil, fmt.Errorf("unknown window %q: use 1Y, 3Y, 5Y or 10Y", windowLabel)
	}

	// ── 1. Extract valid NAV values (reverse to oldest-first) ────────────────
	navs := validNAVs(fund.NAVs) // oldest-first, zero/negative stripped
	if len(navs) < days+1 {
		return nil, fmt.Errorf(
			"not enough data for %s: need %d points, have %d",
			windowLabel, days+1, len(navs),
		)
	}

	// ── 2. Data availability ──────────────────────────────────────────────────
	avail := dataAvailability(fund.NAVs, len(navs))

	// ── 3. Rolling simple returns ─────────────────────────────────────────────
	rollingReturns := rollingSimpleReturns(navs, days)

	// ── 4. Rolling CAGR ───────────────────────────────────────────────────────
	years := float64(days) / 252.0
	rollingCAGRs := rollingCAGR(navs, days, years)

	// ── 5. Max drawdown (full series) ─────────────────────────────────────────
	mdd := maxDrawdown(navs)

	return &model.AnalyticsResult{
		FundCode:               fund.Code,
		FundName:               fund.Name,
		Window:                 windowLabel,
		DataAvailability:       avail,
		RollingPeriodsAnalyzed: len(rollingReturns),
		RollingDays:            days,
		Rolling:                computeExtendedStats(rollingReturns),
		MaxDrawdown:            mdd,
		CAGR:                   computeStats(rollingCAGRs),
	}, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// validNAVs strips zero/negative entries and reverses to oldest-first.
func validNAVs(entries []model.NAVEntry) []float64 {
	out := make([]float64, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- { // reverse: oldest first
		if entries[i].NAV > 0 {
			out = append(out, entries[i].NAV)
		}
	}
	return out
}

// dataAvailability builds the DataAvailability block from the raw NAV slice.
// entries is latest-first (as stored in Fund.NAVs).
func dataAvailability(entries []model.NAVEntry, validCount int) model.DataAvailability {
	if len(entries) == 0 {
		return model.DataAvailability{}
	}

	endDate := entries[0].Date                    // latest (index 0)
	startDate := entries[len(entries)-1].Date     // oldest (last index)

	totalDays := 0
	t1, err1 := time.Parse("02-01-2006", startDate)
	t2, err2 := time.Parse("02-01-2006", endDate)
	if err1 == nil && err2 == nil {
		totalDays = int(t2.Sub(t1).Hours() / 24)
	}

	return model.DataAvailability{
		StartDate:     startDate,
		EndDate:       endDate,
		TotalDays:     totalDays,
		NAVDataPoints: validCount,
	}
}

// rollingSimpleReturns computes (end−start)/start*100 for every window.
// navs is oldest-first.
func rollingSimpleReturns(navs []float64, window int) []float64 {
	results := make([]float64, 0, len(navs)-window)
	for i := 0; i+window < len(navs); i++ {
		start, end := navs[i], navs[i+window]
		results = append(results, (end-start)/start*100)
	}
	return results
}

// rollingCAGR computes the annualised return for every rolling window.
// navs is oldest-first.
func rollingCAGR(navs []float64, window int, years float64) []float64 {
	results := make([]float64, 0, len(navs)-window)
	for i := 0; i+window < len(navs); i++ {
		start, end := navs[i], navs[i+window]
		cagr := (math.Pow(end/start, 1/years) - 1) * 100
		results = append(results, cagr)
	}
	return results
}

// maxDrawdown scans the full series for the worst peak-to-trough drop.
// navs is oldest-first. Returns a negative percentage (e.g. -34.7).
func maxDrawdown(navs []float64) float64 {
	if len(navs) == 0 {
		return 0
	}
	peak := navs[0]
	mdd := 0.0
	for _, v := range navs {
		if v > peak {
			peak = v
		}
		if peak > 0 {
			dd := (v - peak) / peak * 100
			if dd < mdd {
				mdd = dd
			}
		}
	}
	return mdd
}

// computeStats returns min, max and median — used for CAGR.
func computeStats(data []float64) model.Stats {
	if len(data) == 0 {
		return model.Stats{}
	}
	sorted := sortedCopy(data)
	n := len(sorted)
	return model.Stats{
		Min:    sorted[0],
		Max:    sorted[n-1],
		Median: median(sorted),
	}
}

// computeExtendedStats returns min, max, median, p25, p75 — used for rolling returns.
func computeExtendedStats(data []float64) model.ExtendedStats {
	if len(data) == 0 {
		return model.ExtendedStats{}
	}
	sorted := sortedCopy(data)
	n := len(sorted)
	return model.ExtendedStats{
		Min:    sorted[0],
		Max:    sorted[n-1],
		Median: median(sorted),
		P25:    sorted[n*25/100],
		P75:    sorted[n*75/100],
	}
}

// sortedCopy returns a sorted copy of data without mutating the original.
func sortedCopy(data []float64) []float64 {
	cp := make([]float64, len(data))
	copy(cp, data)
	sort.Float64s(cp)
	return cp
}

// median assumes sorted input.
func median(sorted []float64) float64 {
	n := len(sorted)
	if n%2 == 0 {
		return (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return sorted[n/2]
}