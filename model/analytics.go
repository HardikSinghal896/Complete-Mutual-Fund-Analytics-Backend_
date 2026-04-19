package model

// Stats holds min / max / median — used for CAGR.
type Stats struct {
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Median float64 `json:"median"`
}

// ExtendedStats adds 25th and 75th percentiles — used for rolling returns.
type ExtendedStats struct {
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Median float64 `json:"median"`
	P25    float64 `json:"p25"`
	P75    float64 `json:"p75"`
}

// DataAvailability describes the NAV date range actually used.
type DataAvailability struct {
	StartDate     string `json:"start_date"`      // oldest NAV date DD-MM-YYYY
	EndDate       string `json:"end_date"`        // latest NAV date DD-MM-YYYY
	TotalDays     int    `json:"total_days"`      // calendar days between start and end
	NAVDataPoints int    `json:"nav_data_points"` // valid NAV entries used
}

// AnalyticsResult is the full response for GET /funds/{code}/analytics.
type AnalyticsResult struct {
	// Basic identity
	FundCode string `json:"fund_code"`
	FundName string `json:"fund_name"`
	Category string `json:"category"` // filled by handler
	AMC      string `json:"amc"`      // filled by handler
	Window   string `json:"window"`

	// NAV data coverage
	DataAvailability DataAvailability `json:"data_availability"`

	// Rolling return stats (includes p25/p75)
	RollingPeriodsAnalyzed int           `json:"rolling_periods_analyzed"`
	RollingDays            int           `json:"rolling_days"`
	Rolling                ExtendedStats `json:"rolling_returns"`

	// Risk
	MaxDrawdown float64 `json:"max_drawdown_pct"`

	// Annualised return
	CAGR Stats `json:"cagr_pct"`

	// Meta
	ComputedAt string `json:"computed_at"` // filled by handler, RFC3339 UTC
}