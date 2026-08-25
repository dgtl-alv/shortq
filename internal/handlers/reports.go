package handlers

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"shortq/internal/models"
)

func (h *Handler) linkReports(w http.ResponseWriter, r *http.Request) {
	h.reportRequest(w, r, "link", strings.TrimPrefix(r.URL.Path, "/api/v1/reports/links/"))
}

func (h *Handler) userReports(w http.ResponseWriter, r *http.Request) {
	h.reportRequest(w, r, "user", strings.TrimPrefix(r.URL.Path, "/api/v1/reports/users/"))
}

func (h *Handler) reportRequest(w http.ResponseWriter, r *http.Request, kind, tail string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.Trim(tail, "/"), "/")
	if len(parts) < 1 || len(parts) > 2 || parts[0] == "" {
		errOut(w, http.StatusNotFound, "report not found")
		return
	}
	u, _ := userFrom(r.Context())
	var id int64
	var err error
	if kind == "user" && parts[0] == "me" {
		id = u.ID
	} else {
		id, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil || id < 1 {
			errOut(w, http.StatusNotFound, "report not found")
			return
		}
	}
	from, to, loc, err := parseReportWindow(r.URL.Query())
	if err != nil {
		errOut(w, http.StatusBadRequest, err.Error())
		return
	}
	format := ""
	if len(parts) == 2 {
		format = parts[1]
	}
	if format == "events.csv" {
		events, err := h.S.ReportEvents(u, kind, id, from, to)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				errOut(w, http.StatusNotFound, "report not found")
			} else {
				errOut(w, http.StatusBadRequest, err.Error())
			}
			return
		}
		h.writeEventsCSV(w, kind, id, events, loc)
		return
	}
	report, err := h.buildReport(u, kind, id, from, to, loc)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			errOut(w, http.StatusNotFound, "report not found")
		} else {
			errOut(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	switch format {
	case "":
		jsonOut(w, http.StatusOK, report)
	case "summary.csv":
		h.writeSummaryCSV(w, kind, id, report)
	default:
		errOut(w, http.StatusNotFound, "report not found")
	}
}

func (h *Handler) buildReport(u models.User, kind string, id int64, from, to time.Time, loc *time.Location) (models.AnalyticsReport, error) {
	if kind == "link" {
		return h.S.LinkReport(u, id, from, to, loc)
	}
	return h.S.UserReport(u, id, from, to, loc)
}

func parseReportWindow(values url.Values) (time.Time, time.Time, *time.Location, error) {
	zone := strings.TrimSpace(values.Get("timezone"))
	if zone == "" {
		zone = "UTC"
	}
	if len(zone) > 80 {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("timezone is too long")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("timezone must be a valid IANA name")
	}
	now := time.Now().In(loc)
	fromDay := strings.TrimSpace(values.Get("from"))
	toDay := strings.TrimSpace(values.Get("to"))
	if fromDay == "" && toDay == "" {
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -29)
		end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
		return start.UTC(), end.UTC(), loc, nil
	}
	if fromDay == "" || toDay == "" {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("from and to must be supplied together")
	}
	fromLocal, err := time.ParseInLocation("2006-01-02", fromDay, loc)
	if err != nil {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("from must be YYYY-MM-DD")
	}
	toLocal, err := time.ParseInLocation("2006-01-02", toDay, loc)
	if err != nil {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("to must be YYYY-MM-DD")
	}
	if !fromLocal.Before(toLocal) {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("from must be before the exclusive to date")
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	oldest := today.AddDate(0, 0, -89)
	if fromLocal.Before(oldest) {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("detailed reports are available for the latest 90 local days")
	}
	if toLocal.After(today.AddDate(0, 0, 1)) {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("to may not be after tomorrow")
	}
	days := 0
	for day := fromLocal; day.Before(toLocal); day = day.AddDate(0, 0, 1) {
		days++
		if days > 90 {
			return time.Time{}, time.Time{}, nil, fmt.Errorf("report range may contain at most 90 days")
		}
	}
	return fromLocal.UTC(), toLocal.UTC(), loc, nil
}

func (h *Handler) writeSummaryCSV(w http.ResponseWriter, kind string, id int64, report models.AnalyticsReport) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="shortq-%s-%d-summary.csv"`, kind, id))
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"section", "metric", "label", "value"})
	summary := []struct {
		key string
		val any
	}{
		{"all_time_clicks", report.Summary.AllTimeClicks}, {"period_clicks", report.Summary.PeriodClicks},
		{"human_clicks", report.Summary.HumanClicks}, {"bot_clicks", report.Summary.BotClicks},
		{"estimated_unique_visitors", report.Summary.EstimatedUniqueVisitors},
		{"expired_attempts", report.Summary.ExpiredAttempts}, {"average_clicks_per_day", report.Summary.AverageClicksPerDay},
		{"peak_day", report.Summary.PeakDay}, {"peak_day_clicks", report.Summary.PeakDayClicks},
		{"active_links", report.Summary.ActiveLinks}, {"deleted_links", report.Summary.DeletedLinks},
		{"private_links", report.Summary.PrivateLinks}, {"shared_links", report.Summary.SharedLinks},
	}
	for _, item := range summary {
		_ = writer.Write([]string{"summary", item.key, item.key, safeCSVCell(fmt.Sprint(item.val))})
	}
	for section, points := range report.Breakdowns {
		for _, point := range points {
			_ = writer.Write([]string{"breakdown", section, safeCSVCell(point.Key), strconv.FormatInt(point.Clicks, 10)})
		}
	}
	for _, point := range report.Daily {
		_ = writer.Write([]string{"daily", "clicks", point.Day, strconv.FormatInt(point.Clicks, 10)})
	}
	writer.Flush()
}

func (h *Handler) writeEventsCSV(w http.ResponseWriter, kind string, id int64, events []models.SafeClickEvent, loc *time.Location) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="shortq-%s-%d-events.csv"`, kind, id))
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"timestamp", "link_id", "slug", "country", "device", "browser", "os", "referrer_host", "utm_source", "utm_medium", "utm_campaign", "route", "is_bot", "status"})
	for _, event := range events {
		row := []string{
			event.CreatedAt.In(loc).Format(time.RFC3339), strconv.FormatInt(event.LinkID, 10), event.Slug,
			event.CountryCode, event.Device, event.Browser, event.OS, event.ReferrerHost, event.UTMSource,
			event.UTMMedium, event.UTMCampaign, event.RouteType, strconv.FormatBool(event.IsBot), strconv.Itoa(event.StatusCode),
		}
		for index := range row {
			row[index] = safeCSVCell(row[index])
		}
		_ = writer.Write(row)
	}
	writer.Flush()
}
