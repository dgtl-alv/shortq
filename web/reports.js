let currentReportState = null;

function canManageLink(link) {
  return Boolean(currentUser && (currentUser.role === "superadmin" || currentUser.role === "tenant" || link.user_id === currentUser.id));
}

function reportEsc(value) {
  return esc(value == null ? "" : value);
}

function localDateValue(date) {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return year + "-" + month + "-" + day;
}

function setReportPreset(value, reload = true) {
  if (value === "custom") return;
  const days = Number(value || 30);
  const end = new Date();
  const start = new Date(end);
  start.setDate(end.getDate() - days + 1);
  document.querySelector("#reportFrom").value = localDateValue(start);
  document.querySelector("#reportTo").value = localDateValue(end);
  if (reload && currentReportState) loadCurrentReport();
}

function reportQuery() {
  const endExclusive = new Date(document.querySelector("#reportTo").value + "T00:00:00");
  endExclusive.setDate(endExclusive.getDate() + 1);
  return new URLSearchParams({
    from: document.querySelector("#reportFrom").value,
    to: localDateValue(endExclusive),
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"
  });
}

function openLinkReport(id) {
  location.hash = "report/link/" + id;
}

function openUserReport(id) {
  location.hash = "report/user/" + (id == null ? "me" : id);
}

function backToDashboard() {
  location.hash = "app";
}

async function routeView() {
  if (!currentUser) return;
  const match = location.hash.match(/^#?report\/(link|user)\/([a-z0-9]+)$/i);
  if (!match) {
    currentReportState = null;
    document.querySelector("#dashboardHome").hidden = false;
    document.querySelector("#reportView").hidden = true;
    return;
  }
  currentReportState = { kind: match[1].toLowerCase(), id: match[2] };
  document.querySelector("#dashboardHome").hidden = true;
  document.querySelector("#reportView").hidden = false;
  if (!document.querySelector("#reportFrom").value) setReportPreset("30", false);
  await loadCurrentReport();
}

function reportBasePath() {
  if (!currentReportState) return "";
  const plural = currentReportState.kind === "link" ? "links" : "users";
  return "/api/v1/reports/" + plural + "/" + currentReportState.id;
}

async function loadCurrentReport() {
  if (!currentReportState) return;
  document.querySelector("#reportTitle").textContent = "Loading report...";
  document.querySelector("#reportSubtitle").textContent = "";
  document.querySelector("#reportSummary").innerHTML = '<div class="empty">Loading metrics...</div>';
  document.querySelector("#reportCharts").innerHTML = "";
  try {
    const report = await api(reportBasePath() + "?" + reportQuery().toString());
    renderReport(report);
  } catch (error) {
    document.querySelector("#reportTitle").textContent = "Report unavailable";
    document.querySelector("#reportSummary").innerHTML = "";
    showMsg(error);
  }
}

function downloadReport(format) {
  if (!currentReportState) return;
  location.href = reportBasePath() + "/" + format + "?" + reportQuery().toString();
}

function reportNumber(value, digits) {
  return Number(value || 0).toLocaleString(undefined, { maximumFractionDigits: digits == null ? 0 : digits });
}

function reportCard(label, value, note) {
  return "<article><span>" + reportEsc(label) + "</span><b>" + reportEsc(value) + "</b><small>" + reportEsc(note) + "</small></article>";
}

function renderReport(report) {
  showMsg();
  const subject = report.subject || {};
  const isLink = subject.type === "link";
  document.querySelector("#reportTitle").textContent = isLink ? (subject.title || "/" + subject.slug) : subject.name;
  document.querySelector("#reportSubtitle").textContent = isLink
    ? "/" + subject.slug + " · " + (subject.visibility === "department" ? "shared with ALVA" : "private") + " · owned by " + subject.owner_name
    : subject.email + " · owned-link performance";
  document.querySelector("#reportCoverage").textContent =
    "Showing " + report.from + " through the day before " + report.to + " in " + report.timezone +
    ". Detailed events are retained for " + report.detail_retained_days +
    " days. Unique visitors are estimated from distinct non-bot IP addresses; deleted links remain in owner reports.";

  const summary = report.summary || {};
  const cards = [
    ["Period clicks", reportNumber(summary.period_clicks), summary.previous_period_clicks == null ? "Successful redirects" : reportNumber(summary.change_percent, 1) + "% vs prior period"],
    ["All-time clicks", reportNumber(summary.all_time_clicks), isLink ? "Successful redirects" : "Across owned links"],
    ["Human clicks", reportNumber(summary.human_clicks), "Heuristic non-bot traffic"],
    ["Estimated visitors", reportNumber(summary.estimated_unique_visitors), "Distinct non-bot IPs"],
    ["Bot clicks", reportNumber(summary.bot_clicks), "User-agent heuristic"],
    ["Expired attempts", reportNumber(summary.expired_attempts), "Tracked unavailable redirects"],
    ["Daily average", reportNumber(summary.average_clicks_per_day, 1), "Successful redirects per day"],
    ["Peak day", summary.peak_day || "—", reportNumber(summary.peak_day_clicks) + " clicks"]
  ];
  if (!isLink) {
    cards.push(["Owned links", reportNumber((summary.active_links || 0) + (summary.deleted_links || 0)), reportNumber(summary.active_links) + " active · " + reportNumber(summary.deleted_links) + " deleted"]);
  }
  document.querySelector("#reportSummary").innerHTML = cards.map(function(card) {
    return reportCard(card[0], card[1], card[2]);
  }).join("");

  const breakdowns = report.breakdowns || {};
  const charts = [
    lineChartCard("Clicks by day", "Successful redirects in the selected local-time period.", report.daily || []),
    compositionChartCard("Human and bot traffic", "Successful redirects classified from user-agent strings.", breakdowns.traffic || [])
  ];
  const chartMap = [
    ["country", "Countries", "Cloudflare country signal for successful redirects."],
    ["device", "Devices", "Client device class for successful redirects."],
    ["browser", "Browsers", "Detected browser family."],
    ["os", "Operating systems", "Detected operating-system family."],
    ["referrer", "Referrer hosts", "Unknown includes direct and privacy-restricted traffic."],
    ["utm_source", "UTM sources", "Source parameter present on resolved destinations."],
    ["utm_medium", "UTM media", "Medium parameter present on resolved destinations."],
    ["campaign", "UTM campaigns", "Campaign parameter present on resolved destinations."],
    ["route", "Routing outcomes", "Default, device, country, and expired routing decisions."],
    ["status", "HTTP outcomes", "Tracked redirect and expired status codes."]
  ];
  chartMap.forEach(function(item) {
    charts.push(barChartCard(item[1], item[2], breakdowns[item[0]] || []));
  });
  document.querySelector("#reportCharts").innerHTML = charts.join("");

  const top = report.top_links || [];
  document.querySelector("#reportTopLinksCard").hidden = !top.length;
  document.querySelector("#reportTopLinks").innerHTML = top.map(function(link) {
    return '<div class="row one-action"><div><b>' + reportEsc(link.title || link.slug) + '</b><div class="tiny">/' +
      reportEsc(link.slug) + " · " + reportNumber(link.period_clicks) + " period · " + reportNumber(link.all_time_clicks) +
      " all-time · " + reportEsc(link.deleted_at ? "deleted" : link.visibility) +
      '</div></div><button class="secondary" onclick="openLinkReport(' + link.id + ')">Open report</button></div>';
  }).join("");

  const events = report.recent_events || [];
  document.querySelector("#reportEvents").innerHTML = events.map(function(event) {
    return "<tr><td>" + reportEsc(new Date(event.created_at).toLocaleString()) + "</td><td>/" + reportEsc(event.slug) +
      "</td><td>" + reportEsc(event.country_code || "unknown") + "</td><td>" + reportEsc(event.device) + " · " +
      reportEsc(event.browser) + " · " + reportEsc(event.os) + "</td><td>" + reportEsc(event.referrer_host || "unknown") +
      "</td><td>" + reportEsc(event.utm_campaign || "unknown") + "</td><td>" + (event.is_bot ? "bot · " : "") +
      reportEsc(event.route_type) + " → " + event.status_code + "</td></tr>";
  }).join("") || '<tr><td colspan="7">No events in this period.</td></tr>';
}

function chartDataTable(points, labelKey) {
  labelKey = labelKey || "key";
  return '<details class="chart-data"><summary>View data</summary><table><thead><tr><th>Label</th><th>Clicks</th></tr></thead><tbody>' +
    points.map(function(point) {
      return "<tr><td>" + reportEsc(point[labelKey]) + "</td><td>" + reportNumber(point.clicks) + "</td></tr>";
    }).join("") + "</tbody></table></details>";
}

function lineChartCard(title, subtitle, points) {
  const width = 720, height = 230, left = 42, right = 18, top = 18, bottom = 34;
  const max = Math.max.apply(null, [1].concat(points.map(function(point) { return Number(point.clicks || 0); })));
  const span = Math.max(1, points.length - 1);
  const coords = points.map(function(point, index) {
    return {
      x: left + (index / span) * (width - left - right),
      y: top + (1 - Number(point.clicks || 0) / max) * (height - top - bottom),
      day: point.day,
      clicks: point.clicks
    };
  });
  if (points.length < 2) return chartCard(title, subtitle, '<div class="empty">Not enough days for a trend.</div>');
  const polyline = coords.map(function(point) { return point.x.toFixed(1) + "," + point.y.toFixed(1); }).join(" ");
  const labels = coords.filter(function(point, index) { return index === 0 || index === coords.length - 1 || index === Math.floor(coords.length / 2); });
  let svg = '<svg class="chart-svg" viewBox="0 0 ' + width + " " + height + '" role="img" aria-label="' + reportEsc(title) + '">';
  svg += '<line x1="' + left + '" y1="' + (height - bottom) + '" x2="' + (width - right) + '" y2="' + (height - bottom) + '" class="chart-axis"/>';
  svg += '<line x1="' + left + '" y1="' + top + '" x2="' + left + '" y2="' + (height - bottom) + '" class="chart-axis"/>';
  svg += '<line x1="' + left + '" y1="' + top + '" x2="' + (width - right) + '" y2="' + top + '" class="chart-grid"/>';
  svg += '<text x="' + (left - 8) + '" y="' + (top + 4) + '" text-anchor="end" class="chart-label">' + reportNumber(max) + "</text>";
  svg += '<text x="' + (left - 8) + '" y="' + (height - bottom + 4) + '" text-anchor="end" class="chart-label">0</text>';
  svg += '<polyline points="' + polyline + '" class="chart-line"/>';
  svg += coords.map(function(point) {
    return '<circle cx="' + point.x + '" cy="' + point.y + '" r="3.5" class="chart-point"><title>' +
      reportEsc(point.day) + ": " + reportNumber(point.clicks) + " clicks</title></circle>";
  }).join("");
  svg += labels.map(function(point) {
    const anchor = point.x === left ? "start" : (point.x > width / 2 ? "end" : "middle");
    return '<text x="' + point.x + '" y="' + (height - 10) + '" text-anchor="' + anchor + '" class="chart-label">' +
      reportEsc(point.day.slice(5)) + "</text>";
  }).join("") + "</svg>";
  return chartCard(title, subtitle, svg + chartDataTable(points, "day"));
}

function barChartCard(title, subtitle, points) {
  const shown = (points || []).slice(0, 8);
  if (!shown.length) return chartCard(title, subtitle, '<div class="empty">No data in this period.</div>');
  const max = Math.max.apply(null, [1].concat(shown.map(function(point) { return Number(point.clicks || 0); })));
  const rows = shown.map(function(point) {
    const width = Math.max(2, (point.clicks / max) * 100);
    return '<div class="bar-row"><span title="' + reportEsc(point.key) + '">' + reportEsc(point.key) +
      '</span><div><i style="width:' + width + '%"></i></div><b>' + reportNumber(point.clicks) + "</b></div>";
  }).join("");
  return chartCard(title, subtitle, '<div class="bar-chart">' + rows + "</div>" + chartDataTable(shown));
}

function compositionChartCard(title, subtitle, points) {
  const values = {};
  (points || []).forEach(function(point) { values[point.key] = Number(point.clicks || 0); });
  const human = values.human || 0, bot = values.bot || 0, total = human + bot;
  if (!total) return chartCard(title, subtitle, '<div class="empty">No successful redirects in this period.</div>');
  const humanShare = (human / total) * 100;
  const visual = '<div class="composition-chart" role="img" aria-label="' + reportNumber(humanShare, 1) +
    " percent human and " + reportNumber(100 - humanShare, 1) +
    ' percent bot traffic"><i style="width:' + humanShare + '%"></i><em style="width:' + (100 - humanShare) +
    '%"></em></div><div class="composition-legend"><span><i></i>Human ' + reportNumber(human) + " (" +
    reportNumber(humanShare, 1) + '%)</span><span><em></em>Bot ' + reportNumber(bot) + " (" +
    reportNumber(100 - humanShare, 1) + "%)</span></div>";
  return chartCard(title, subtitle, visual + chartDataTable(points));
}

function chartCard(title, subtitle, body) {
  return '<section class="card chart-card"><h3>' + reportEsc(title) + '</h3><p class="muted">' +
    reportEsc(subtitle) + "</p>" + body + "</section>";
}

window.addEventListener("hashchange", function() {
  routeView().catch(showMsg);
});
