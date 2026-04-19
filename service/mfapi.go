package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const mfAPIBase = "https://api.mfapi.in/mf"

// mfAPIResponse mirrors the JSON returned by GET /mf/{code}.
type mfAPIResponse struct {
	Status string `json:"status"`
	Meta   struct {
		FundHouse      string `json:"fund_house"`
		SchemeType     string `json:"scheme_type"`
		SchemeCategory string `json:"scheme_category"`
		SchemeCode     int    `json:"scheme_code"`
		SchemeName     string `json:"scheme_name"`
	} `json:"meta"`
	Data []struct {
		Date string `json:"date"` // "DD-MM-YYYY"
		NAV  string `json:"nav"`  // decimal string
	} `json:"data"`
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

// fetchScheme calls the mfapi.in API for a single scheme code and returns
// the parsed response.
func fetchScheme(code string) (*mfAPIResponse, error) {
	url := fmt.Sprintf("%s/%s", mfAPIBase, code)

	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned HTTP %d", url, resp.StatusCode)
	}

	var result mfAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if result.Status != "SUCCESS" {
		return nil, fmt.Errorf("API status: %s", result.Status)
	}

	return &result, nil
}

// parseNAV converts the NAV string from the API to float64.
func parseNAV(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}
