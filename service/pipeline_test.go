package service

import (
	"os"
	"testing"
	"time"

	"mf-mvp/model"
)

// ── Pure helper tests (no DB required) ───────────────────────────────────────

func TestToMySQLDate_ValidInput(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"01-01-2020", "2020-01-01"},
		{"31-12-2023", "2023-12-31"},
		{"15-08-1947", "1947-08-15"},
	}
	for _, c := range cases {
		got, err := toMySQLDate(c.in)
		if err != nil {
			t.Errorf("toMySQLDate(%q): unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("toMySQLDate(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToMySQLDate_InvalidInput(t *testing.T) {
	bad := []string{"2020-01-01", "not-a-date", "", "32-01-2020"}
	for _, b := range bad {
		_, err := toMySQLDate(b)
		if err == nil {
			t.Errorf("toMySQLDate(%q): expected error, got nil", b)
		}
	}
}

func TestParseAPINavs_CapLimit(t *testing.T) {
	resp := &mfAPIResponse{}
	for i := 0; i < 200; i++ {
		resp.Data = append(resp.Data, struct {
			Date string `json:"date"`
			NAV  string `json:"nav"`
		}{Date: "01-01-2020", NAV: "100.00"})
	}
	navs := parseAPINavs(resp, 50)
	if len(navs) != 50 {
		t.Errorf("parseAPINavs: expected 50 entries, got %d", len(navs))
	}
}

func TestParseAPINavs_SkipsInvalidNAV(t *testing.T) {
	resp := &mfAPIResponse{
		Data: []struct {
			Date string `json:"date"`
			NAV  string `json:"nav"`
		}{
			{Date: "03-01-2020", NAV: "100.00"},
			{Date: "02-01-2020", NAV: "not-a-number"},
			{Date: "01-01-2020", NAV: "90.00"},
		},
	}
	navs := parseAPINavs(resp, 10)
	if len(navs) != 2 {
		t.Errorf("expected 2 valid entries, got %d", len(navs))
	}
}

func TestParseAPINavs_Empty(t *testing.T) {
	resp := &mfAPIResponse{}
	navs := parseAPINavs(resp, 100)
	if len(navs) != 0 {
		t.Errorf("expected empty result, got %d entries", len(navs))
	}
}

// TestIncrementalCutoff verifies the date-comparison logic used inside
// saveToDB. We replicate the cutoff condition in isolation so it can be
// validated without a DB connection.
func TestIncrementalCutoff_SkipsOldRows(t *testing.T) {
	lastDate, _ := time.Parse("2006-01-02", "2024-01-10")

	rows := []struct {
		date    string
		wantNew bool
	}{
		{"2024-01-09", false}, // before cutoff → skip
		{"2024-01-10", false}, // equal to cutoff → skip
		{"2024-01-11", true},  // after cutoff → insert
		{"2024-01-12", true},  // after cutoff → insert
	}

	for _, r := range rows {
		mysqlDate, err := toMySQLDate(convertAPIDate(r.date))
		if err != nil {
			t.Fatalf("toMySQLDate(%q): %v", r.date, err)
		}
		rowDate, _ := time.Parse("2006-01-02", mysqlDate)
		isNew := rowDate.After(lastDate)
		if isNew != r.wantNew {
			t.Errorf("date %q: isNew=%v, want %v", r.date, isNew, r.wantNew)
		}
	}
}

// convertAPIDate is a local helper that converts "YYYY-MM-DD" (test input)
// to "DD-MM-YYYY" (API format) so toMySQLDate can parse it.
func convertAPIDate(yyyymmdd string) string {
	t, err := time.Parse("2006-01-02", yyyymmdd)
	if err != nil {
		return ""
	}
	return t.Format("02-01-2006")
}

// ── DB integration tests (require MF_DSN env var) ────────────────────────────

// dbOrSkip returns an initialised DB connection or skips the test if MF_DSN
// is not set. This prevents CI failures in environments without MySQL.
func dbOrSkip(t *testing.T) {
	t.Helper()
	if os.Getenv("MF_DSN") == "" {
		t.Skip("skipping DB test: MF_DSN not set")
	}
}

func TestSaveToDB_Idempotent(t *testing.T) {
	dbOrSkip(t)

	conn, err := InitDB()
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	svc := &FundService{db: conn, limiter: NewRateLimiter(), syncStatus: &SyncStatus{Status: "idle"}}
	svc.funds = make(map[string]*model.Fund)

	const testCode = "TEST_IDEMPOTENT"
	resp := &mfAPIResponse{
		Meta: struct {
			FundHouse      string `json:"fund_house"`
			SchemeType     string `json:"scheme_type"`
			SchemeCategory string `json:"scheme_category"`
			SchemeCode     int    `json:"scheme_code"`
			SchemeName     string `json:"scheme_name"`
		}{SchemeName: "Test Fund"},
		Data: []struct {
			Date string `json:"date"`
			NAV  string `json:"nav"`
		}{
			{Date: "10-01-2024", NAV: "100.00"},
			{Date: "09-01-2024", NAV: "99.00"},
			{Date: "08-01-2024", NAV: "98.00"},
		},
	}

	// Insert once — all 3 rows should be new.
	if err := svc.saveToDB(testCode, resp, time.Time{}); err != nil {
		t.Fatalf("first saveToDB: %v", err)
	}

	// Insert again — INSERT IGNORE must not error or duplicate.
	if err := svc.saveToDB(testCode, resp, time.Time{}); err != nil {
		t.Fatalf("second saveToDB (idempotent): %v", err)
	}

	// Verify exactly 3 rows stored.
	var count int
	row := conn.QueryRow(`SELECT COUNT(*) FROM nav_data WHERE scheme_code = ?`, testCode)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 rows after idempotent inserts, got %d", count)
	}

	// Cleanup.
	conn.Exec(`DELETE FROM nav_data WHERE scheme_code = ?`, testCode)
}

func TestSaveToDB_IncrementalSkipsOldRows(t *testing.T) {
	dbOrSkip(t)

	conn, err := InitDB()
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	svc := &FundService{db: conn, limiter: NewRateLimiter(), syncStatus: &SyncStatus{Status: "idle"}}
	svc.funds = make(map[string]*model.Fund)

	const testCode = "TEST_INCREMENTAL"

	// Seed 2 rows as the "existing" data.
	seed := &mfAPIResponse{
		Data: []struct {
			Date string `json:"date"`
			NAV  string `json:"nav"`
		}{
			{Date: "09-01-2024", NAV: "99.00"},
			{Date: "08-01-2024", NAV: "98.00"},
		},
	}
	svc.saveToDB(testCode, seed, time.Time{})

	// Incremental sync: cutoff = 2024-01-09. Only 2024-01-10 is new.
	lastDate, _ := time.Parse("2006-01-02", "2024-01-09")
	update := &mfAPIResponse{
		Data: []struct {
			Date string `json:"date"`
			NAV  string `json:"nav"`
		}{
			{Date: "10-01-2024", NAV: "100.00"}, // new
			{Date: "09-01-2024", NAV: "99.00"},  // old → skip
			{Date: "08-01-2024", NAV: "98.00"},  // old → skip
		},
	}
	if err := svc.saveToDB(testCode, update, lastDate); err != nil {
		t.Fatalf("incremental saveToDB: %v", err)
	}

	// Should be exactly 3 distinct rows.
	var count int
	conn.QueryRow(`SELECT COUNT(*) FROM nav_data WHERE scheme_code = ?`, testCode).Scan(&count)
	if count != 3 {
		t.Errorf("expected 3 rows after incremental insert, got %d", count)
	}

	// Cleanup.
	conn.Exec(`DELETE FROM nav_data WHERE scheme_code = ?`, testCode)
}
