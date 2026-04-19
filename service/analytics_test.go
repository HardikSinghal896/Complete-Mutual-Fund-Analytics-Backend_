package service

import (
	"math"
	"testing"

	"mf-mvp/model"
)

const floatTol = 0.01 // 0.01% tolerance for float comparisons

func approxEqual(a, b, tol float64) bool {
	if b == 0 {
		return math.Abs(a) < tol
	}
	return math.Abs((a-b)/b) < tol
}

// makeLinearFund builds a Fund whose NAV grows linearly from startNAV by
// stepPerDay over nPoints days. Data is stored latest-first (as the real
// pipeline stores it) so index 0 is the most recent value.
//
// Linear growth gives us exact expected values to assert against.
func makeLinearFund(nPoints int, startNAV, stepPerDay float64) *model.Fund {
	navs := make([]model.NAVEntry, nPoints)
	for i := 0; i < nPoints; i++ {
		// oldest entry has the lowest NAV; latest has the highest.
		// Stored latest-first so index 0 = latest.
		day := nPoints - 1 - i
		navs[i] = model.NAVEntry{
			Date: "01-01-2020", // date value not used in calculations
			NAV:  startNAV + float64(day)*stepPerDay,
		}
	}
	return &model.Fund{Code: "TEST", Name: "Test Fund", NAVs: navs}
}

// makeConstantFund builds a Fund with every NAV equal to value.
// Useful for asserting zero return and zero drawdown.
func makeConstantFund(nPoints int, value float64) *model.Fund {
	navs := make([]model.NAVEntry, nPoints)
	for i := range navs {
		navs[i] = model.NAVEntry{Date: "01-01-2020", NAV: value}
	}
	return &model.Fund{Code: "FLAT", Name: "Flat Fund", NAVs: navs}
}

// ── Analyze() ────────────────────────────────────────────────────────────────

func TestAnalyze_InvalidWindow(t *testing.T) {
	fund := makeLinearFund(300, 100, 0.1)
	_, err := Analyze(fund, "2Y")
	if err == nil {
		t.Error("expected error for unknown window '2Y', got nil")
	}
}

func TestAnalyze_InsufficientData(t *testing.T) {
	// 1Y requires 253 points; give it only 100.
	fund := makeLinearFund(100, 100, 0.1)
	_, err := Analyze(fund, "1Y")
	if err == nil {
		t.Error("expected error for insufficient data, got nil")
	}
}

func TestAnalyze_EmptyNAVs(t *testing.T) {
	fund := &model.Fund{Code: "EMPTY", Name: "Empty", NAVs: nil}
	_, err := Analyze(fund, "1Y")
	if err == nil {
		t.Error("expected error for empty NAV slice, got nil")
	}
}

func TestAnalyze_RollingReturn_LinearGrowth(t *testing.T) {
	// 300 points, NAV grows from 100 to 100 + 299*0.5 = 249.5
	// For window=1Y (252 days):
	//   first rolling period: start=navs[0]=100, end=navs[252]= 100+252*0.5=226
	//   simple return = (226-100)/100 * 100 = 126%
	//   last rolling period:  start=navs[47]=123.5, end=navs[299]=249.5
	//   simple return = (249.5-123.5)/123.5 * 100 ≈ 102.02%
	fund := makeLinearFund(300, 100, 0.5)
	result, err := Analyze(fund, "1Y")
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	expectedFirst := (226.0 - 100.0) / 100.0 * 100 // 126%
	if !approxEqual(result.Rolling.Max, expectedFirst, floatTol) {
		t.Errorf("rolling max: got %.4f, want %.4f", result.Rolling.Max, expectedFirst)
	}

	expectedLast := (249.5 - 123.5) / 123.5 * 100 // ≈102.02%
	if !approxEqual(result.Rolling.Min, expectedLast, floatTol) {
		t.Errorf("rolling min: got %.4f, want %.4f", result.Rolling.Min, expectedLast)
	}
}

func TestAnalyze_RollingReturn_FlatNAV(t *testing.T) {
	fund := makeConstantFund(300, 50.0)
	result, err := Analyze(fund, "1Y")
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if !approxEqual(result.Rolling.Min, 0, floatTol) ||
		!approxEqual(result.Rolling.Max, 0, floatTol) ||
		!approxEqual(result.Rolling.Median, 0, floatTol) {
		t.Errorf("flat NAV should yield 0 rolling returns, got min=%.4f max=%.4f",
			result.Rolling.Min, result.Rolling.Max)
	}
}

func TestAnalyze_MaxDrawdown_NoDrawdown(t *testing.T) {
	// Monotonically increasing NAV → drawdown should be 0.
	fund := makeLinearFund(300, 100, 1)
	result, err := Analyze(fund, "1Y")
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	if result.MaxDrawdown < -floatTol {
		t.Errorf("monotonic growth should give 0 drawdown, got %.4f", result.MaxDrawdown)
	}
}

func TestAnalyze_MaxDrawdown_KnownDrop(t *testing.T) {
	// Manually craft a series with a 50% drawdown.
	// Pad to 300 points (>253 needed for 1Y) and embed the peak/trough.
	full := make([]model.NAVEntry, 300)
	for i := range full {
		full[i] = model.NAVEntry{Date: "01-01-2020", NAV: 150}
	}
	// latest=index0=100, peak=index150=200, oldest=index299=100
	full[0] = model.NAVEntry{Date: "03-01-2020", NAV: 100}
	full[149] = model.NAVEntry{Date: "02-01-2020", NAV: 200}
	full[299] = model.NAVEntry{Date: "01-01-2020", NAV: 50}

	fund := &model.Fund{Code: "DD", Name: "Drawdown Fund", NAVs: full}
	result, err := Analyze(fund, "1Y")
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	// Max drawdown must be ≤ -50% given the 200→100 peak-to-trough.
	if result.MaxDrawdown > -49 {
		t.Errorf("expected max drawdown ≤ -49%%, got %.4f", result.MaxDrawdown)
	}
}

func TestAnalyze_CAGR_FlatNAV(t *testing.T) {
	fund := makeConstantFund(300, 100.0)
	result, err := Analyze(fund, "1Y")
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	if !approxEqual(result.CAGR.Median, 0, floatTol) {
		t.Errorf("flat NAV should yield 0 CAGR, got %.4f", result.CAGR.Median)
	}
}

func TestAnalyze_DataPoints(t *testing.T) {
	const n = 300
	fund := makeLinearFund(n, 100, 0.1)
	result, err := Analyze(fund, "1Y")
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	if result.DataAvailability.NAVDataPoints != n {
		t.Errorf("NAVDataPoints: got %d, want %d", result.DataAvailability.NAVDataPoints, n)
	}
	// 1Y = 252 trading days; rolling periods = 300 - 252 = 48
	if result.RollingPeriodsAnalyzed != n-252 {
		t.Errorf("RollingPeriodsAnalyzed: got %d, want %d",
			result.RollingPeriodsAnalyzed, n-252)
	}
}

func TestAnalyze_PercentileOrdering(t *testing.T) {
	fund := makeLinearFund(300, 100, 0.5)
	result, err := Analyze(fund, "1Y")
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	r := result.Rolling
	if !(r.Min <= r.P25 && r.P25 <= r.Median && r.Median <= r.P75 && r.P75 <= r.Max) {
		t.Errorf("percentile ordering violated: min=%.2f p25=%.2f median=%.2f p75=%.2f max=%.2f",
			r.Min, r.P25, r.Median, r.P75, r.Max)
	}
}

// ── Internal helpers ─────────────────────────────────────────────────────────

func TestMaxDrawdown_Flat(t *testing.T) {
	navs := []float64{100, 100, 100, 100}
	if dd := maxDrawdown(navs); dd != 0 {
		t.Errorf("flat series: expected 0, got %v", dd)
	}
}

func TestMaxDrawdown_HalfDrop(t *testing.T) {
	// oldest-first: 100, 200, 100 → peak 200, trough 100 → -50%
	navs := []float64{100, 200, 100}
	dd := maxDrawdown(navs)
	if !approxEqual(dd, -50, floatTol) {
		t.Errorf("expected -50%%, got %.4f", dd)
	}
}

func TestComputeStats_Basic(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5}
	s := computeStats(data)
	if s.Min != 1 || s.Max != 5 || s.Median != 3 {
		t.Errorf("got min=%.0f max=%.0f median=%.0f", s.Min, s.Max, s.Median)
	}
}

func TestComputeExtendedStats_Percentiles(t *testing.T) {
	// 100 elements: 1..100 sorted. p25=data[25]=26, p75=data[75]=76
	data := make([]float64, 100)
	for i := range data {
		data[i] = float64(i + 1)
	}
	s := computeExtendedStats(data)
	if s.P25 != 26 {
		t.Errorf("p25: got %.0f, want 26", s.P25)
	}
	if s.P75 != 76 {
		t.Errorf("p75: got %.0f, want 76", s.P75)
	}
}

func TestValidNAVs_SkipsInvalid(t *testing.T) {
	entries := []model.NAVEntry{
		{Date: "03-01-2020", NAV: 100},
		{Date: "02-01-2020", NAV: -5},  // invalid
		{Date: "01-01-2020", NAV: 0},   // invalid
	}
	result := validNAVs(entries)
	// validNAVs reverses to oldest-first; only the 100 entry is valid
	if len(result) != 1 || result[0] != 100 {
		t.Errorf("expected [100] after stripping invalid entries, got %v", result)
	}
}

func TestValidNAVs_ReversesOrder(t *testing.T) {
	// Input is latest-first; output must be oldest-first.
	entries := []model.NAVEntry{
		{Date: "03-01-2020", NAV: 300},
		{Date: "02-01-2020", NAV: 200},
		{Date: "01-01-2020", NAV: 100},
	}
	result := validNAVs(entries)
	if result[0] != 100 || result[2] != 300 {
		t.Errorf("expected oldest-first [100 200 300], got %v", result)
	}
}