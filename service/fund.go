package service

import (
	"database/sql"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"mf-mvp/model"
)

const fetchDelay = 500 * time.Millisecond

// largeLimitCodes fetch up to 1 000 NAV rows from the DB (and API).
// All other schemes use the original 100-row cap.
var largeLimitCodes = map[string]bool{
	"120591": true,
	"120381": true,
	"118989": true,
	"130503": true,
	"120505": true,
	"125354": true,
    "125497": true,
    "119716": true,
    "120164": true,
    "119775": true,
}

var schemeCodes = []string{
	"120591", // ICICI Prudential Smallcap
	"120381", // ICICI Prudential Midcap
	"118989", // HDFC Mid Cap
	"130503", // HDFC Small Cap
	"120505", // Axis Midcap
	"125354", // Axis Small Cap
	"125497", // SBI Small Cap
	"119716", // SBI Midcap
	"120164", // Kotak Small Cap
	"119775", // Kotak Midcap
}

// FundService holds the in-memory fund store and exposes read operations.
type FundService struct {
	mu         sync.RWMutex
	funds      map[string]*model.Fund
	limiter    *RateLimiter
	db         *sql.DB
	syncStatus *SyncStatus
}

// NewFundService creates the service and immediately loads data.
func NewFundService() (*FundService, error) {
	conn, err := InitDB()
	if err != nil {
		return nil, fmt.Errorf("database init: %w", err)
	}

	svc := &FundService{
		funds:      make(map[string]*model.Fund, len(schemeCodes)),
		limiter:    NewRateLimiter(),
		db:         conn,
		syncStatus: &SyncStatus{Status: "idle"},
	}
	if err := svc.loadAll(); err != nil {
		return nil, err
	}
	return svc, nil
}

// navLimit returns how many NAV rows to fetch for a given scheme code.
func navLimit(code string) int {
	if largeLimitCodes[code] {
		return 2500
	}
	return 100
}

// loadAll fetches NAV data for every tracked scheme.
func (s *FundService) loadAll() error {
	for i, code := range schemeCodes {
		if i > 0 {
			time.Sleep(fetchDelay)
		}

		log.Printf("[api] fetching scheme %s ...", code)
		fund, err := s.fetchAndBuild(code)
		if err != nil {
			return fmt.Errorf("load scheme %s: %w", code, err)
		}

		s.mu.Lock()
		s.funds[code] = fund
		s.mu.Unlock()

		log.Printf("loaded %s (%s) - %d NAV entries", code, fund.Name, len(fund.NAVs))
	}
	return nil
}

// fetchAndBuild syncs a scheme from the API and returns a Fund.
//
// First run (no DB rows): full backfill — all API rows are stored.
// Subsequent runs:        incremental — only rows newer than the latest
//                         DB date are inserted.
//
// In both cases, in-memory NAVs are loaded from DB capped at navLimit.
func (s *FundService) fetchAndBuild(code string) (*model.Fund, error) {
	limit := navLimit(code)

	// Resume point: sync_state is authoritative; latestDateInDB is the
	// fallback for schemes that have nav_data but no sync_state row yet
	// (e.g. written before this feature was added / crash mid-first-run).
	lastDate, err := s.readSyncState(code)
	if err != nil {
		return nil, err
	}

	if lastDate.IsZero() {
		log.Printf("[sync] backfill start for %s", code)
	} else {
		log.Printf("[sync] resume from date %s for %s", lastDate.Format("2006-01-02"), code)
	}

	// Fetch from API (with retry).
	s.limiter.Wait()
	resp, err := fetchSchemeWithRetry(code)
	if err != nil {
		// Persist failure status without changing last_synced_date.
		_ = s.writeSyncState(code, lastDate, "failed")
		log.Printf("[sync] failed for %s: %v", code, err)
		return nil, err
	}

	// Persist new rows. saveToDB skips anything on or before lastDate.
	if saveErr := s.saveToDB(code, resp, lastDate); saveErr != nil {
		log.Printf("[db] save warning for %s: %v", code, saveErr)
		_ = s.writeSyncState(code, lastDate, "failed")
	} else {
		// Record the latest NAV date now in DB as the new resume point.
		latest, dbErr := s.latestDateInDB(code)
		if dbErr == nil && !latest.IsZero() {
			_ = s.writeSyncState(code, latest, "success")
			log.Printf("[sync] success for %s (synced up to %s)", code, latest.Format("2006-01-02"))
		}
	}

	// Load in-memory slice from DB, capped at limit (unchanged behaviour).
	navs, err := s.loadFromDB(code, limit)
	if err != nil || len(navs) == 0 {
		navs = parseAPINavs(resp, limit) // fallback: use API data directly
	}

	return &model.Fund{
		Code: code,
		Name: resp.Meta.SchemeName,
		NAVs: navs,
	}, nil
}

// latestDateInDB returns the most recent NAV date stored for a scheme.
// Returns a zero time.Time when no rows exist (first run / backfill).
func (s *FundService) latestDateInDB(code string) (time.Time, error) {
	const q = `SELECT MAX(date) FROM nav_data WHERE scheme_code = ?`
	var t sql.NullTime
	if err := s.db.QueryRow(q, code).Scan(&t); err != nil {
		return time.Time{}, fmt.Errorf("latestDateInDB: %w", err)
	}
	if !t.Valid {
		return time.Time{}, nil // no rows yet
	}
	return t.Time, nil
}

// fetchSchemeWithRetry calls fetchScheme up to 3 times (1 + 2 retries),
// sleeping 500 ms between attempts.
func fetchSchemeWithRetry(code string) (*mfAPIResponse, error) {
	const maxRetries = 2
	const retryDelay = 500 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("[sync] retry attempt %d for scheme %s", attempt, code)
			time.Sleep(retryDelay)
		}
		resp, err := fetchScheme(code)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("all retries failed for %s: %w", code, lastErr)
}

// loadFromDB reads up to limit rows for the given scheme, ordered latest first.
func (s *FundService) loadFromDB(code string, limit int) ([]model.NAVEntry, error) {
	const q = `
		SELECT date, nav
		FROM   nav_data
		WHERE  scheme_code = ?
		ORDER  BY date DESC
		LIMIT  ?`

	rows, err := s.db.Query(q, code, limit)
	if err != nil {
		return nil, fmt.Errorf("loadFromDB query: %w", err)
	}
	defer rows.Close()

	var navs []model.NAVEntry
	for rows.Next() {
		var (
			date time.Time
			nav  float64
		)
		if err := rows.Scan(&date, &nav); err != nil {
			continue
		}
		navs = append(navs, model.NAVEntry{
			Date: date.Format("02-01-2006"),
			NAV:  nav,
		})
	}
	return navs, rows.Err()
}

// saveToDB inserts NAV rows from resp into the database.
//
//   - Backfill  (lastDate.IsZero()): every row is inserted.
//   - Incremental (!lastDate.IsZero()): rows on or before lastDate are skipped.
//
// INSERT IGNORE means duplicate rows (same scheme_code + date PK) are
// silently discarded, so re-runs are always safe.
func (s *FundService) saveToDB(code string, resp *mfAPIResponse, lastDate time.Time) error {
	if len(resp.Data) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	const q = `INSERT IGNORE INTO nav_data (scheme_code, date, nav) VALUES (?, ?, ?)`
	stmt, err := tx.Prepare(q)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	inserted, skipped := 0, 0
	for _, d := range resp.Data {
		nav, err := parseNAV(d.NAV)
		if err != nil {
			continue
		}
		mysqlDate, err := toMySQLDate(d.Date)
		if err != nil {
			continue
		}

		// Incremental: skip rows that are already in the DB.
		if !lastDate.IsZero() {
			rowDate, err := time.Parse("2006-01-02", mysqlDate)
			if err == nil && !rowDate.After(lastDate) {
				skipped++
				continue
			}
		}

		if _, err := stmt.Exec(code, mysqlDate, nav); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert: %w", err)
		}
		inserted++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	log.Printf("[sync] %s — inserted %d new rows, skipped %d old rows", code, inserted, skipped)
	return nil
}

// toMySQLDate converts "DD-MM-YYYY" to "YYYY-MM-DD".
func toMySQLDate(apiDate string) (string, error) {
	t, err := time.Parse("02-01-2006", apiDate)
	if err != nil {
		return "", err
	}
	return t.Format("2006-01-02"), nil
}

// parseAPINavs converts an API response to []NAVEntry (latest first), capped at limit.
func parseAPINavs(resp *mfAPIResponse, limit int) []model.NAVEntry {
	navs := make([]model.NAVEntry, 0, limit)
	for _, d := range resp.Data {
		if len(navs) >= limit {
			break
		}
		nav, err := parseNAV(d.NAV)
		if err != nil {
			continue
		}
		navs = append(navs, model.NAVEntry{Date: d.Date, NAV: nav})
	}
	return navs
}

// fetchSchemeName calls the API solely to retrieve the scheme name.
func (s *FundService) fetchSchemeName(code string) (string, error) {
	s.limiter.Wait()
	resp, err := fetchScheme(code)
	if err != nil {
		return "", err
	}
	return resp.Meta.SchemeName, nil
}

// fundName looks up the name of an already-loaded fund (read-safe).
func (s *FundService) fundName(code string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if f, ok := s.funds[code]; ok {
		return f.Name
	}
	return ""
}

// ListFunds returns a snapshot of all tracked funds.
func (s *FundService) ListFunds() []*model.Fund {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*model.Fund, 0, len(s.funds))
	for _, f := range s.funds {
		list = append(list, f)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Code < list[j].Code })
	return list
}

// GetFund returns a single fund by scheme code, or (nil, false) if not found.
func (s *FundService) GetFund(code string) (*model.Fund, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.funds[code]
	return f, ok
}

// RankedFunds returns all funds sorted by SimpleReturn descending.
func (s *FundService) RankedFunds() []*model.Fund {
	funds := s.ListFunds()
	sort.Slice(funds, func(i, j int) bool {
		return funds[i].SimpleReturn() > funds[j].SimpleReturn()
	})
	return funds
}

// ── Analytics-based ranking ───────────────────────────────────────────────────

// fundCategories maps every tracked scheme code to its category string.
// Kept here as a simple lookup — no model change needed.
var fundCategories = map[string]string{
	"120591": "smallcap",
	"120381": "midcap",
	"118989": "midcap",
	"130503": "smallcap",
	"120505": "midcap",
	"125354": "smallcap",
	"125497": "smallcap",
	"119716": "midcap",
	"120164": "smallcap",
	"119775": "midcap",
}

// RankedFundResult carries the analytics values needed for one ranked entry.
type RankedFundResult struct {
	Code         string
	Name         string
	MedianReturn float64
	MaxDrawdown  float64
	CurrentNAV   float64
	LastUpdated  string
}

// RankedByAnalyticsResult is the full payload returned to the handler.
type RankedByAnalyticsResult struct {
	TotalFunds int
	Funds      []RankedFundResult
}

// RankedByAnalytics filters funds by category, runs Analyze on each,
// sorts by sortBy, and returns the top `limit` results.
func (s *FundService) RankedByAnalytics(category, window, sortBy string, limit int) (*RankedByAnalyticsResult, error) {
	all := s.ListFunds()

	// Filter by category; collect analytics for each matching fund.
	type scored struct {
		r RankedFundResult
	}
	var candidates []RankedFundResult

	for _, f := range all {
		if fundCategories[f.Code] != category {
			continue
		}
		result, err := Analyze(f, window)
		if err != nil {
			log.Printf("[rank] skipping %s: %v", f.Code, err)
			continue
		}

		lastUpdated := ""
		if len(f.NAVs) > 0 {
			lastUpdated = f.NAVs[0].Date
		}

		candidates = append(candidates, RankedFundResult{
			Code:         f.Code,
			Name:         f.Name,
			MedianReturn: result.Rolling.Median,
			MaxDrawdown:  result.MaxDrawdown,
			CurrentNAV:   f.LatestNAV(),
			LastUpdated:  lastUpdated,
		})
	}

	if len(candidates) == 0 {
		return &RankedByAnalyticsResult{TotalFunds: 0, Funds: []RankedFundResult{}}, nil
	}

	// Sort.
	sort.Slice(candidates, func(i, j int) bool {
		if sortBy == "max_drawdown" {
			// Less negative (closer to 0) is better → ascending absolute value
			return candidates[i].MaxDrawdown > candidates[j].MaxDrawdown
		}
		// median_return: higher is better → descending
		return candidates[i].MedianReturn > candidates[j].MedianReturn
	})

	// Cap at limit.
	if limit > len(candidates) {
		limit = len(candidates)
	}

	return &RankedByAnalyticsResult{
		TotalFunds: len(candidates),
		Funds:      candidates[:limit],
	}, nil
}

// Reload re-fetches all scheme data (DB-first, then API for misses).
// Called by the background refresh goroutine in main.go every 6 hours.
func (s *FundService) Reload() error {
	return s.loadAll()
}

// FundCategory returns the category label for a scheme code.
// Falls back to "Equity" for unknown codes.
func FundCategory(code string) string {
	if cat, ok := fundCategories[code]; ok {
		return cat
	}
	return "Equity"
}

// ── Sync pipeline ─────────────────────────────────────────────────────────────

// SyncStatus tracks the state of the most recent (or in-progress) data sync.
type SyncStatus struct {
	mu          sync.RWMutex
	running     bool
	Status      string    // idle | running | failed | completed
	LastRun     time.Time
	LastSuccess time.Time
	Error       string
}

// TriggerSync starts loadAll() in a goroutine if one is not already running.
// Returns true if a new sync was started, false if already in progress.
func (s *FundService) TriggerSync() bool {
	s.syncStatus.mu.Lock()
	if s.syncStatus.running {
		s.syncStatus.mu.Unlock()
		return false
	}
	s.syncStatus.running = true
	s.syncStatus.Status = "running"
	s.syncStatus.LastRun = time.Now()
	s.syncStatus.Error = ""
	s.syncStatus.mu.Unlock()

	go func() {
		log.Println("[sync] started")
		err := s.loadAll()

		s.syncStatus.mu.Lock()
		defer s.syncStatus.mu.Unlock()
		s.syncStatus.running = false
		if err != nil {
			s.syncStatus.Status = "failed"
			s.syncStatus.Error = err.Error()
			log.Printf("[sync] failed: %v", err)
		} else {
			s.syncStatus.Status = "completed"
			s.syncStatus.LastSuccess = time.Now()
			log.Println("[sync] completed")
		}
	}()

	return true
}

// GetSyncStatus returns a snapshot of the current sync state.
func (s *FundService) GetSyncStatus() (status, errMsg string, lastRun, lastSuccess time.Time) {
	s.syncStatus.mu.RLock()
	defer s.syncStatus.mu.RUnlock()
	return s.syncStatus.Status, s.syncStatus.Error,
		s.syncStatus.LastRun, s.syncStatus.LastSuccess
}

// ── Sync state persistence ────────────────────────────────────────────────────

// readSyncState returns the last successfully synced date for a scheme.
//
// Priority:
//  1. sync_state table  — authoritative resume point across restarts.
//  2. MAX(date) in nav_data — fallback for schemes with data but no state row
//     (first upgrade, or crash before first writeSyncState call).
//  3. Zero time — triggers a full backfill.
func (s *FundService) readSyncState(code string) (time.Time, error) {
	const q = `SELECT last_synced_date FROM sync_state WHERE scheme_code = ?`
	var t sql.NullTime
	err := s.db.QueryRow(q, code).Scan(&t)
	if err == nil && t.Valid {
		return t.Time, nil
	}
	// No sync_state row — fall back to nav_data max date.
	return s.latestDateInDB(code)
}

// writeSyncState upserts the sync outcome for a scheme.
// lastDate should be the latest NAV date on success, or the previous resume
// point on failure (so we do not regress the checkpoint).
func (s *FundService) writeSyncState(code string, lastDate time.Time, status string) error {
	const q = `
		INSERT INTO sync_state (scheme_code, last_synced_date, last_status, updated_at)
		VALUES (?, ?, ?, NOW())
		ON DUPLICATE KEY UPDATE
			last_synced_date = VALUES(last_synced_date),
			last_status      = VALUES(last_status),
			updated_at       = NOW()`

	var dateVal interface{}
	if lastDate.IsZero() {
		dateVal = nil
	} else {
		dateVal = lastDate.Format("2006-01-02")
	}

	if _, err := s.db.Exec(q, code, dateVal, status); err != nil {
		return fmt.Errorf("writeSyncState %s: %w", code, err)
	}
	return nil
}