package handlers

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"shortq/internal/models"
)

func TestParseReportWindowUsesIANAZoneAndExclusiveEnd(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(loc)
	fromDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -6)
	toDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	from, to, gotLoc, err := parseReportWindow(url.Values{
		"timezone": {"Asia/Kolkata"},
		"from":     {fromDay.Format("2006-01-02")},
		"to":       {toDay.Format("2006-01-02")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotLoc.String() != "Asia/Kolkata" || !from.Equal(fromDay.UTC()) || !to.Equal(toDay.UTC()) {
		t.Fatalf("window = %s..%s %s", from, to, gotLoc)
	}
}

func TestParseReportWindowRejectsInvalidAndOverlongRanges(t *testing.T) {
	if _, _, _, err := parseReportWindow(url.Values{"timezone": {"Mars/Olympus"}}); err == nil {
		t.Fatal("invalid timezone accepted")
	}
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -90).Format("2006-01-02")
	to := now.AddDate(0, 0, 1).Format("2006-01-02")
	if _, _, _, err := parseReportWindow(url.Values{"timezone": {"UTC"}, "from": {from}, "to": {to}}); err == nil {
		t.Fatal("range older than retained detail accepted")
	}
}

func TestEventsCSVExcludesSensitiveRequestFields(t *testing.T) {
	h := &Handler{}
	res := httptest.NewRecorder()
	h.writeEventsCSV(res, "link", 7, []models.SafeClickEvent{{
		ID: 1, LinkID: 7, Slug: "=campaign", CountryCode: "ID", Device: "mobile",
		Browser: "Chrome", OS: "Android", ReferrerHost: "example.com", UTMCampaign: "launch",
		RouteType: "default", StatusCode: 302, CreatedAt: time.Now().UTC(),
	}}, time.UTC)
	body := res.Body.String()
	for _, forbidden := range []string{"ip", "user_agent", "resolved_url", "referrer,"} {
		if strings.Contains(strings.SplitN(body, "\n", 2)[0], forbidden) {
			t.Fatalf("sensitive column %q present: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "'=campaign") {
		t.Fatalf("CSV injection prefix missing: %s", body)
	}
}
