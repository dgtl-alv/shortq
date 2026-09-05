package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"shortq/internal/models"
)

const reportRetentionDays = 90

type reportSubject struct {
	kind string
	id   int64
	link *models.Link
	user *models.User
}

func (s *Store) LinkReport(scope models.User, id int64, from, to time.Time, loc *time.Location) (models.AnalyticsReport, error) {
	link, err := s.reportLink(scope, id)
	if err != nil {
		return models.AnalyticsReport{}, err
	}
	subject := reportSubject{kind: "link", id: id, link: &link}
	return s.buildReport(subject, from, to, loc)
}

func (s *Store) UserReport(scope models.User, id int64, from, to time.Time, loc *time.Location) (models.AnalyticsReport, error) {
	user, err := s.reportUser(scope, id)
	if err != nil {
		return models.AnalyticsReport{}, err
	}
	subject := reportSubject{kind: "user", id: id, user: &user}
	return s.buildReport(subject, from, to, loc)
}

func (s *Store) ReportEvents(scope models.User, kind string, id int64, from, to time.Time) ([]models.SafeClickEvent, error) {
	var subject reportSubject
	var err error
	switch kind {
	case "link":
		link, lookupErr := s.reportLink(scope, id)
		err = lookupErr
		subject = reportSubject{kind: kind, id: id, link: &link}
	case "user":
		user, lookupErr := s.reportUser(scope, id)
		err = lookupErr
		subject = reportSubject{kind: kind, id: id, user: &user}
	default:
		return nil, errors.New("unknown report subject")
	}
	if err != nil {
		return nil, err
	}
	return s.safeEvents(subject, from, to, 100001)
}

func (s *Store) reportLink(scope models.User, id int64) (models.Link, error) {
	q := `SELECT ` + linkColumns + ` FROM links WHERE id=?`
	args := []any{id}
	q, args = addLinkViewScope(q, args, scope, "links", false)
	link, err := scanLink(s.DB.QueryRow(q, args...))
	if err != nil {
		return link, err
	}
	links, err := s.hydrateLinks([]models.Link{link})
	if err != nil {
		return link, err
	}
	return links[0], nil
}

func (s *Store) reportUser(scope models.User, id int64) (models.User, error) {
	target, err := s.UserByID(id)
	if err != nil {
		return target, err
	}
	switch scope.Role {
	case "superadmin":
		return target, nil
	case "tenant":
		if scope.TenantID != nil && target.TenantID != nil && *scope.TenantID == *target.TenantID {
			return target, nil
		}
	case "customer":
		if scope.ID == target.ID {
			return target, nil
		}
	}
	return models.User{}, sql.ErrNoRows
}

func reportWhere(subject reportSubject) (string, []any) {
	if subject.kind == "link" {
		return "l.id=?", []any{subject.id}
	}
	return "l.user_id=?", []any{subject.id}
}

func isSuccessfulRedirect(status int, route string) bool {
	return route != "expired" && (status == 301 || status == 302 || status == 307 || status == 308)
}

func (s *Store) buildReport(subject reportSubject, from, to time.Time, loc *time.Location) (models.AnalyticsReport, error) {
	report := models.AnalyticsReport{
		From:               from.In(loc).Format("2006-01-02"),
		To:                 to.In(loc).Format("2006-01-02"),
		Timezone:           loc.String(),
		DetailRetainedDays: reportRetentionDays,
		UniqueIsEstimate:   true,
		Breakdowns:         map[string][]models.AnalyticsPoint{},
		Daily:              []models.ReportDailyPoint{},
		RecentEvents:       []models.SafeClickEvent{},
	}
	where, subjectArgs := reportWhere(subject)
	if subject.kind == "link" {
		owner, err := s.UserByID(subject.link.UserID)
		if err != nil {
			return report, err
		}
		report.Subject = map[string]any{
			"type": "link", "id": subject.link.ID, "slug": subject.link.Slug, "title": subject.link.Title,
			"target_url": subject.link.TargetURL, "visibility": subject.link.Visibility, "clicks": subject.link.Clicks,
			"owner_id": owner.ID, "owner_name": owner.Name, "owner_email": owner.Email,
			"redirect_code": subject.link.RedirectCode, "expires_at": subject.link.ExpiresAt,
			"max_clicks": subject.link.MaxClicks, "created_at": subject.link.CreatedAt, "deleted_at": subject.link.DeletedAt,
			"tags": subject.link.Tags, "utm_source": subject.link.UTMSource, "utm_medium": subject.link.UTMMedium,
			"utm_campaign": subject.link.UTMCampaign,
		}
		report.Summary.AllTimeClicks = subject.link.Clicks
		if subject.link.DeletedAt == nil {
			report.Summary.ActiveLinks = 1
		} else {
			report.Summary.DeletedLinks = 1
		}
		if subject.link.Visibility == "department" {
			report.Summary.SharedLinks = 1
		} else {
			report.Summary.PrivateLinks = 1
		}
	} else {
		report.Subject = map[string]any{
			"type": "user", "id": subject.user.ID, "name": subject.user.Name, "email": subject.user.Email,
			"role": subject.user.Role, "tenant_id": subject.user.TenantID, "active": subject.user.Active,
			"created_at": subject.user.CreatedAt,
		}
		if err := s.DB.QueryRow(`SELECT COALESCE(SUM(clicks),0),
			COUNT(*) FILTER (WHERE deleted_at IS NULL),COUNT(*) FILTER (WHERE deleted_at IS NOT NULL),
			COUNT(*) FILTER (WHERE visibility='private'),COUNT(*) FILTER (WHERE visibility='department')
			FROM links WHERE user_id=?`, subject.id).Scan(
			&report.Summary.AllTimeClicks, &report.Summary.ActiveLinks, &report.Summary.DeletedLinks,
			&report.Summary.PrivateLinks, &report.Summary.SharedLinks); err != nil {
			return report, err
		}
	}

	daily := map[string]*models.ReportDailyPoint{}
	for day := from.In(loc); day.Before(to.In(loc)); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		daily[key] = &models.ReportDailyPoint{Day: key}
	}
	dimensions := map[string]map[string]int64{
		"country": {}, "device": {}, "browser": {}, "os": {}, "referrer": {},
		"utm_source": {}, "utm_medium": {}, "campaign": {}, "route": {}, "status": {}, "traffic": {},
	}
	q := `SELECT c.created_at,c.status_code,c.route_type,c.browser,c.os,c.device,c.is_bot,
		c.country_code,c.referrer_host,c.utm_source,c.utm_medium,c.utm_campaign
		FROM clicks c JOIN links l ON l.id=c.link_id WHERE ` + where + ` AND c.created_at>=? AND c.created_at<?`
	args := append(append([]any{}, subjectArgs...), from.UTC(), to.UTC())
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var created time.Time
		var status int
		var route, browser, osName, device, country, referrer, source, medium, campaign string
		var bot bool
		if err := rows.Scan(&created, &status, &route, &browser, &osName, &device, &bot, &country, &referrer, &source, &medium, &campaign); err != nil {
			return report, err
		}
		if route == "expired" || status == 410 {
			report.Summary.ExpiredAttempts++
		}
		dimensions["route"][reportKey(route)]++
		dimensions["status"][strconv.Itoa(status)]++
		if !isSuccessfulRedirect(status, route) {
			continue
		}
		report.Summary.PeriodClicks++
		point := daily[created.In(loc).Format("2006-01-02")]
		if point != nil {
			point.Clicks++
		}
		if bot {
			report.Summary.BotClicks++
			dimensions["traffic"]["bot"]++
			if point != nil {
				point.Bots++
			}
		} else {
			report.Summary.HumanClicks++
			dimensions["traffic"]["human"]++
			if point != nil {
				point.Human++
			}
		}
		for dimension, value := range map[string]string{
			"country": country, "device": device, "browser": browser, "os": osName,
			"referrer": referrer, "utm_source": source, "utm_medium": medium, "campaign": campaign,
		} {
			dimensions[dimension][reportKey(value)]++
		}
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	for day := from.In(loc); day.Before(to.In(loc)); day = day.AddDate(0, 0, 1) {
		point := daily[day.Format("2006-01-02")]
		report.Daily = append(report.Daily, *point)
		if point.Clicks > report.Summary.PeakDayClicks {
			report.Summary.PeakDayClicks = point.Clicks
			report.Summary.PeakDay = point.Day
		}
	}
	if len(report.Daily) > 0 {
		report.Summary.AverageClicksPerDay = float64(report.Summary.PeriodClicks) / float64(len(report.Daily))
	}
	for name, counts := range dimensions {
		report.Breakdowns[name] = sortedPoints(counts, 10)
	}

	uniqueQ := `SELECT COUNT(DISTINCT CASE WHEN c.is_bot=FALSE AND c.route_type<>'expired'
		AND c.status_code IN (301,302,307,308) THEN NULLIF(c.ip,'') END)
		FROM clicks c JOIN links l ON l.id=c.link_id WHERE ` + where + ` AND c.created_at>=? AND c.created_at<?`
	if err := s.DB.QueryRow(uniqueQ, args...).Scan(&report.Summary.EstimatedUniqueVisitors); err != nil {
		return report, err
	}

	daysInWindow := len(report.Daily)
	previousFrom, previousTo := from.In(loc).AddDate(0, 0, -daysInWindow).UTC(), from
	if !previousFrom.Before(time.Now().UTC().AddDate(0, 0, -reportRetentionDays)) {
		previousQ := `SELECT COUNT(*) FROM clicks c JOIN links l ON l.id=c.link_id WHERE ` + where +
			` AND c.created_at>=? AND c.created_at<? AND c.route_type<>'expired' AND c.status_code IN (301,302,307,308)`
		previousArgs := append(append([]any{}, subjectArgs...), previousFrom.UTC(), previousTo.UTC())
		var previous int64
		if err := s.DB.QueryRow(previousQ, previousArgs...).Scan(&previous); err != nil {
			return report, err
		}
		report.Summary.PreviousPeriodClicks = &previous
		if previous > 0 {
			change := (float64(report.Summary.PeriodClicks-previous) / float64(previous)) * 100
			report.Summary.ChangePercent = &change
		}
	}

	if subject.kind == "user" {
		topQ := `SELECT l.id,l.slug,l.title,l.visibility,l.clicks,l.deleted_at,
			COALESCE(SUM(CASE WHEN c.route_type<>'expired' AND c.status_code IN (301,302,307,308) THEN 1 ELSE 0 END),0) period_clicks
			FROM links l LEFT JOIN clicks c ON c.link_id=l.id AND c.created_at>=? AND c.created_at<?
			WHERE l.user_id=? GROUP BY l.id,l.slug,l.title,l.visibility,l.clicks,l.deleted_at
			ORDER BY period_clicks DESC,l.clicks DESC,l.id DESC LIMIT 20`
		topRows, err := s.DB.Query(topQ, from.UTC(), to.UTC(), subject.id)
		if err != nil {
			return report, err
		}
		for topRows.Next() {
			var rank models.ReportLinkRank
			var deleted sql.NullTime
			if err := topRows.Scan(&rank.ID, &rank.Slug, &rank.Title, &rank.Visibility, &rank.AllTimeClicks, &deleted, &rank.PeriodClicks); err != nil {
				topRows.Close()
				return report, err
			}
			if deleted.Valid {
				rank.DeletedAt = &deleted.Time
			}
			report.TopLinks = append(report.TopLinks, rank)
		}
		if err := topRows.Err(); err != nil {
			topRows.Close()
			return report, err
		}
		topRows.Close()
	}

	report.RecentEvents, err = s.safeEvents(subject, from, to, 50)
	return report, err
}

func reportKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func sortedPoints(counts map[string]int64, limit int) []models.AnalyticsPoint {
	points := make([]models.AnalyticsPoint, 0, len(counts))
	for key, clicks := range counts {
		points = append(points, models.AnalyticsPoint{Key: key, Clicks: clicks})
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].Clicks == points[j].Clicks {
			return points[i].Key < points[j].Key
		}
		return points[i].Clicks > points[j].Clicks
	})
	if len(points) > limit {
		var other int64
		for _, point := range points[limit-1:] {
			other += point.Clicks
		}
		points = append(points[:limit-1], models.AnalyticsPoint{Key: "Other", Clicks: other})
	}
	return points
}

func (s *Store) safeEvents(subject reportSubject, from, to time.Time, limit int) ([]models.SafeClickEvent, error) {
	where, subjectArgs := reportWhere(subject)
	q := `SELECT c.id,c.link_id,l.slug,c.country_code,c.status_code,c.route_type,c.browser,c.os,c.device,
		c.is_bot,c.referrer_host,c.utm_source,c.utm_medium,c.utm_campaign,c.created_at
		FROM clicks c JOIN links l ON l.id=c.link_id WHERE ` + where +
		` AND c.created_at>=? AND c.created_at<? ORDER BY c.id DESC LIMIT ?`
	args := append(append([]any{}, subjectArgs...), from.UTC(), to.UTC(), limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.SafeClickEvent{}
	for rows.Next() {
		var event models.SafeClickEvent
		if err := rows.Scan(&event.ID, &event.LinkID, &event.Slug, &event.CountryCode, &event.StatusCode,
			&event.RouteType, &event.Browser, &event.OS, &event.Device, &event.IsBot, &event.ReferrerHost,
			&event.UTMSource, &event.UTMMedium, &event.UTMCampaign, &event.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	if len(out) >= 100001 {
		return nil, fmt.Errorf("event export exceeds 100000 rows; choose a shorter date range")
	}
	return out, rows.Err()
}
