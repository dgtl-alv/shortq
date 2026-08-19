package handlers

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"shortq/internal/models"
)

func TestIsReservedRootPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/", true},
		{"/app.js", true},
		{"/style.css", true},
		{"/favicon.ico", true},
		{"/api/v1/me", true},
		{"/nested/slug", true},
		{"/image.png", true},
		{"/abc123", false},
		{"/promo-alva", false},
	}
	for _, tt := range tests {
		if got := isReservedRootPath(tt.path); got != tt.want {
			t.Fatalf("isReservedRootPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestResolveTargetPrecedence(t *testing.T) {
	link := models.Link{TargetURL: "https://default.example", IOSURL: "https://ios.example", AndroidURL: "https://android.example", GeoTargets: []models.GeoTarget{{CountryCode: "ID", TargetURL: "https://id.example"}}}
	if target, route := resolveTarget(link, "ID", "iPhone"); target != "https://id.example" || route != "country" {
		t.Fatalf("geo precedence got %s %s", target, route)
	}
	if target, route := resolveTarget(link, "SG", "iPhone"); target != "https://ios.example" || route != "ios" {
		t.Fatalf("iOS route got %s %s", target, route)
	}
}

func TestMergeTargetQueryInboundWins(t *testing.T) {
	link := models.Link{ForwardQuery: true, UTMSource: "configured", UTMCampaign: "launch"}
	got := mergeTargetQuery("https://example.org/path?keep=yes&utm_source=old", link, url.Values{"utm_source": {"incoming"}, "ref": {"42"}})
	parsed, _ := url.Parse(got)
	if parsed.Query().Get("utm_source") != "incoming" || parsed.Query().Get("utm_campaign") != "launch" || parsed.Query().Get("keep") != "yes" {
		t.Fatalf("merged query = %s", got)
	}
}

func TestImagePDF(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	pdf, err := imagePDF(img)
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF-1.4")) || !bytes.HasSuffix(pdf, []byte("%%EOF\n")) {
		t.Fatalf("invalid PDF: err=%v len=%d", err, len(pdf))
	}
}

func TestApplyLinkPayloadAcceptsTargetURLAlias(t *testing.T) {
	target := "https://updated.example/path"
	link, _, _, err := applyLinkPayload(models.Link{TargetURL: "https://old.example", RedirectCode: 302}, linkPayload{TargetURL: &target}, false)
	if err != nil || link.TargetURL != target {
		t.Fatalf("target_url alias failed: link=%#v err=%v", link, err)
	}
}

func TestApplyLinkPayloadRejectsConflictingURLAliases(t *testing.T) {
	urlValue, targetValue := "https://one.example", "https://two.example"
	_, _, _, err := applyLinkPayload(models.Link{}, linkPayload{URL: &urlValue, TargetURL: &targetValue}, true)
	if err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("expected alias conflict, got %v", err)
	}
}

func TestDecodeRejectsUnknownAndTrailingJSON(t *testing.T) {
	for _, body := range []string{`{"url":"https://example.org","typo":true}`, `{"url":"https://example.org"}{}`} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		res := httptest.NewRecorder()
		var payload linkPayload
		if decode(res, req, &payload) || res.Code != http.StatusBadRequest {
			t.Fatalf("decode accepted %q with status %d", body, res.Code)
		}
	}
}

func TestQRRejectsWrongMethod(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/qr?text=test", nil)
	res := httptest.NewRecorder()
	h.qr(res, req)
	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("QR POST status = %d, want 405", res.Code)
	}
}

func TestAddCenteredLogo(t *testing.T) {
	qrImage := image.NewRGBA(image.Rect(0, 0, 200, 200))
	draw.Draw(qrImage, qrImage.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	logo := image.NewRGBA(image.Rect(0, 0, 80, 20))
	draw.Draw(logo, logo.Bounds(), image.NewUniform(color.RGBA{R: 0, G: 168, B: 181, A: 255}), image.Point{}, draw.Src)

	got := addCenteredLogo(qrImage, logo)
	if got.Bounds() != qrImage.Bounds() {
		t.Fatalf("bounds = %v, want %v", got.Bounds(), qrImage.Bounds())
	}
	center := color.RGBAModel.Convert(got.At(100, 100)).(color.RGBA)
	if center.G != 168 || center.B != 181 {
		t.Fatalf("center pixel = %#v, want ALVA logo color", center)
	}
	badge := color.RGBAModel.Convert(got.At(74, 93)).(color.RGBA)
	if badge != (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Fatalf("badge pixel = %#v, want white", badge)
	}
	outside := color.RGBAModel.Convert(got.At(72, 100)).(color.RGBA)
	if outside != (color.RGBA{A: 255}) {
		t.Fatalf("pixel outside badge = %#v, want original QR background", outside)
	}
	outsideAbove := color.RGBAModel.Convert(got.At(100, 91)).(color.RGBA)
	if outsideAbove != (color.RGBA{A: 255}) {
		t.Fatalf("pixel above rectangular badge = %#v, want original QR background", outsideAbove)
	}
}

func TestTrimWhiteMargin(t *testing.T) {
	logo := image.NewRGBA(image.Rect(0, 0, 48, 48))
	draw.Draw(logo, logo.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(logo, image.Rect(6, 6, 42, 42), image.NewUniform(color.Black), image.Point{}, draw.Src)

	got := trimWhiteMargin(logo)
	if got.Bounds().Dx() != 36 || got.Bounds().Dy() != 36 {
		t.Fatalf("trimmed bounds = %v, want 36x36", got.Bounds())
	}
	if pixel := color.RGBAModel.Convert(got.At(0, 0)).(color.RGBA); pixel != (color.RGBA{A: 255}) {
		t.Fatalf("trimmed corner = %#v, want logo content", pixel)
	}
}
