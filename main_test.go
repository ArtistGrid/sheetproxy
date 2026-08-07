package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeJobMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		wantMode string
	}{
		{"empty defaults to htmlview", "", "htmlview"},
		{"htmlview stays htmlview", "htmlview", "htmlview"},
		{"preview stays preview", "preview", "preview"},
		{"HTMLVIEW normalized to htmlview", "HTMLVIEW", "htmlview"},
		{"Preview normalized to preview", "Preview", "preview"},
		{"invalid falls back to htmlview", "invalid", "htmlview"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job, err := normalizeJob(jobConfig{
				SheetURL: "https://docs.google.com/spreadsheets/d/abc123",
				Mode:     tt.mode,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if job.Mode != tt.wantMode {
				t.Errorf("Mode = %q, want %q", job.Mode, tt.wantMode)
			}
		})
	}
}

func TestExtractAssetPathsHtmlview(t *testing.T) {
	job := &Job{Mode: "htmlview"}
	html := `<img src="/htmlview/sheet/image1.png">` +
		`<link href="/preview/sheet/style.css">` +
		`<img src="/static/logo.png">`
	got := extractAssetPaths(html, job)
	want := []string{"/htmlview/sheet/image1.png", "/static/logo.png"}
	if len(got) != len(want) {
		t.Fatalf("got %d paths, want %d: %v", len(got), len(want), got)
	}
	for i, p := range got {
		if p != want[i] {
			t.Errorf("path[%d] = %q, want %q", i, p, want[i])
		}
	}
}

func TestExtractAssetPathsPreview(t *testing.T) {
	job := &Job{Mode: "preview"}
	html := `<img src="/htmlview/sheet/image1.png">` +
		`<link href="/preview/sheet/style.css">` +
		`<img src="/static/logo.png">`
	got := extractAssetPaths(html, job)
	want := []string{"/preview/sheet/style.css", "/static/logo.png"}
	if len(got) != len(want) {
		t.Fatalf("got %d paths, want %d: %v", len(got), len(want), got)
	}
	for i, p := range got {
		if p != want[i] {
			t.Errorf("path[%d] = %q, want %q", i, p, want[i])
		}
	}
}

func TestExtractAssetPathsNoModePrefix(t *testing.T) {
	job := &Job{Mode: "htmlview"}
	html := `<img src="/assets/logo.png">`
	got := extractAssetPaths(html, job)
	if len(got) != 0 {
		t.Errorf("expected no matches, got %v", got)
	}
}

func TestInjectSEOMeta(t *testing.T) {
	job := &Job{
		Mode:            "htmlview",
		PageTitle:       "Frank Ocean Tracker",
		PageDescription: "Track all Frank Ocean releases",
		Lang:            "en",
	}
	input := `<html><head>
<title>original</title>
<meta property="og:image" content="/assets/abc123.png">
<meta property="og:type" content="article">
<meta property="og:site_name" content="Google Docs">
<meta property="og:title" content="old">
</head><body>x</body></html>`
	got := injectSEOMeta(input, job)

	want := []string{
		`<html lang="en" dir="ltr">`,
		`<meta name="description" content="Track all Frank Ocean releases">`,
		`<meta name="robots" content="index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1">`,
		`<meta name="googlebot" content="index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1">`,
		`<meta name="bingbot" content="index, follow, max-image-preview:large, max-snippet:-1">`,
		`<link rel="manifest" href="/manifest.webmanifest">`,
		`<link rel="apple-touch-icon" href="/assets/abc123.png">`,
		`<link rel="dns-prefetch" href="https://www.gstatic.com">`,
		`<meta name="author" content="Frank Ocean Tracker">`,
		`<meta name="theme-color" content="#111111" media="(prefers-color-scheme: dark)">`,
		`<meta name="theme-color" content="#ffffff" media="(prefers-color-scheme: light)">`,
		`<meta name="msapplication-TileColor" content="#111111">`,
		`<meta name="dcterms.title" content="Frank Ocean Tracker">`,
		`<meta name="dcterms.creator" content="Frank Ocean Tracker">`,
		`<meta name="dcterms.language" content="en">`,
		`<meta name="dcterms.type" content="Text">`,
		`<meta name="apple-mobile-web-app-capable" content="yes">`,
		`<meta name="keywords" content="Frank Ocean tracker, Frank Ocean discography, Frank Ocean unreleased discography, Frank Ocean unreleased music, Frank Ocean music leaks, Frank Ocean leaks, Frank Ocean grails, Frank Ocean new music, unreleased discography, discography tracker, music leaks, leaks">`,
		`<meta property="og:type" content="website">`,
		`<meta property="og:site_name" content="Frank Ocean Tracker">`,
		`<meta property="og:title" content="Frank Ocean Tracker">`,
		`<meta property="og:description" content="Track all Frank Ocean releases">`,
		`<meta property="og:image" content="/assets/abc123.png">`,
		`<meta property="og:image:alt" content="Frank Ocean Tracker">`,
		`<meta property="og:image:type" content="image/png">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`<meta name="twitter:image" content="/assets/abc123.png">`,
		`<meta name="twitter:image:alt" content="Frank Ocean Tracker">`,
		`<meta name="twitter:title" content="Frank Ocean Tracker">`,
		`application/ld+json`,
		`"@graph"`,
		`"@type":"MusicGroup"`,
		`"name":"Frank Ocean"`,
		`"alternateName":"Frank Ocean Tracker"`,
		`"image":"/assets/abc123.png"`,
		`"knowsAbout":["Frank Ocean discography","Frank Ocean unreleased music","Frank Ocean music leaks","released and unreleased music","tracking leaks"]`,
		`"@type":"WebPage"`,
		`"@type":"WebSite"`,
		`"@type":"Organization"`,
		`"@type":"Dataset"`,
		`"@type":"BreadcrumbList"`,
		`"@type":"ListItem"`,
		`"@type":"Person"`,
		`"member":{"@id":"#person"}`,
		`"memberOf":{"@id":"#artist"}`,
		`"logo"`,
		`"isFamilyFriendly":true`,
		`"primaryImageOfPage"`,
		`"provider":`,
		`"isFamilyFriendly":true`,
		`"@type":"FAQPage"`,
		`"@type":"Question"`,
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q", w)
		}
	}

	notWant := []string{
		`Google Docs`,
		`content="old"`,
		`property="og:type" content="article"`,
		`rel="canonical"`,
		`og:url`,
		`generator`,
		`sheetproxy`,
		`copyright`,
		`published_time`,
	}
	for _, w := range notWant {
		if strings.Contains(got, w) {
			t.Errorf("output still contains %q", w)
		}
	}
}

func TestNormalizeJobLeavesDescriptionEmpty(t *testing.T) {
	job, err := normalizeJob(jobConfig{
		SheetURL:  "https://docs.google.com/spreadsheets/d/abc123",
		PageTitle: "Frank Ocean Tracker",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.PageDescription != "" {
		t.Errorf("PageDescription = %q, want empty so auto-description can generate", job.PageDescription)
	}
}

func TestArtistFromTitle(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Frank Ocean Tracker", "Frank Ocean"},
		{"Deftones Tracker", "Deftones"},
		{"Kanye West Discography Tracker", "Kanye West"},
		{"Radiohead", "Radiohead"},
		{"Frank Ocean", "Frank Ocean"},
	}
	for _, tt := range tests {
		if got := artistFromTitle(tt.in); got != tt.want {
			t.Errorf("artistFromTitle(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestShortName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Tyler, The Creator", "Tyler Tracker"},
		{"Frank Ocean", "Frank Tracker"},
		{"Deftones", "Deftones Tracker"},
	}
	for _, tt := range tests {
		if got := shortName(tt.in); got != tt.want {
			t.Errorf("shortName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestWritePWAOutputs(t *testing.T) {
	dir := t.TempDir()
	job := &Job{
		WwwDir:    dir,
		PageTitle: "Tyler, The Creator Tracker",
		Lang:      "en",
	}
	writePWAOutputs(job, "/assets/og.png")

	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.webmanifest"))
	if err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	for _, w := range []string{
		`"name": "Tyler, The Creator Tracker"`,
		`"short_name": "Tyler Tracker"`,
		`"display": "standalone"`,
		`"start_url": "./"`,
		`"/assets/og.png"`,
	} {
		if !strings.Contains(string(manifest), w) {
			t.Errorf("manifest missing %q", w)
		}
	}

	llms, err := os.ReadFile(filepath.Join(dir, "llms.txt"))
	if err != nil {
		t.Fatalf("llms.txt not written: %v", err)
	}
	if !strings.Contains(string(llms), "Tyler, The Creator") {
		t.Error("llms.txt missing artist")
	}

	llmsFull, err := os.ReadFile(filepath.Join(dir, "llms-full.txt"))
	if err != nil {
		t.Fatalf("llms-full.txt not written: %v", err)
	}
	if !strings.Contains(string(llmsFull), "## Content") {
		t.Error("llms-full.txt missing Content section")
	}

	robots, err := os.ReadFile(filepath.Join(dir, "robots.txt"))
	if err != nil {
		t.Fatalf("robots.txt not written: %v", err)
	}
	if !strings.Contains(string(robots), "Allow: /") {
		t.Error("robots.txt missing Allow")
	}
}

func TestAutoGeneratedDescription(t *testing.T) {
	job := &Job{Mode: "htmlview", PageTitle: "Frank Ocean Tracker", Lang: "en"}
	html := `<html><head><title>t</title></head><body></body></html>`
	got := injectSEOMeta(html, job)
	if !strings.Contains(got, `content="View and explore Frank Ocean&#39;s unreleased and released discography and music leaks on Frank Ocean Tracker."`) {
		t.Errorf("auto description missing: %s", got)
	}
}

func TestEnsureOgImageType(t *testing.T) {
	in := `<head><meta property="og:image" content="/assets/abc.png"></head>`
	got := ensureOgImageType(in)
	if !strings.Contains(got, `<meta property="og:image:type" content="image/png">`) {
		t.Errorf("type not injected: %s", got)
	}
	if strings.Count(got, "og:image:type") != 1 {
		t.Errorf("expected single og:image:type, got %s", got)
	}

	// unknown extension -> left alone
	in2 := `<head><meta property="og:image" content="/assets/abc.xyz"></head>`
	if out := ensureOgImageType(in2); strings.Contains(out, "og:image:type") {
		t.Errorf("should not add type for unknown ext: %s", out)
	}
}

func TestEnsureOgImageTypeReplaces(t *testing.T) {
	in := `<head><meta property="og:image" content="/assets/abc.png"><meta property="og:image:type" content="image/jpeg"></head>`
	got := ensureOgImageType(in)
	if !strings.Contains(got, `content="image/png"`) {
		t.Errorf("expected type to be corrected to png: %s", got)
	}
}

func TestEnsureOgImageDims(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 1200, 630))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "og.png"), buf.Bytes(), 0644)

	in := `<head><meta property="og:image" content="/og.png"></head>`
	got := ensureOgImageDims(in, dir)
	for _, w := range []string{
		`<meta property="og:image:width" content="1200">`,
		`<meta property="og:image:height" content="630">`,
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in %s", w, got)
		}
	}

	// missing file -> untouched
	in2 := `<head><meta property="og:image" content="/nope.png"></head>`
	if out := ensureOgImageDims(in2, dir); strings.Contains(out, "og:image:width") {
		t.Errorf("should not inject dims for missing file: %s", out)
	}
}

func TestPwaIconsGenerated(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 1200, 1200))
	for y := 0; y < 1200; y++ {
		for x := 0; x < 1200; x++ {
			img.Set(x, y, color.RGBA{R: 20, G: 20, B: 20, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "og.png"), buf.Bytes(), 0644)

	job := &Job{WwwDir: dir, FaviconHref: "/favicon.png", PageTitle: "Frank Ocean Tracker", Lang: "en"}
	icons := pwaIcons(job, "/og.png")

	var srcs []string
	for _, ic := range icons {
		srcs = append(srcs, ic["src"])
	}
	for _, w := range []string{"/icon-192.png", "/icon-512.png", "/icon-maskable-512.png", "/og.png"} {
		found := false
		for _, s := range srcs {
			if s == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected icon %q in %v", w, srcs)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "icon-192.png")); err != nil {
		t.Errorf("icon-192.png not written: %v", err)
	}
}

func TestEnsureOgCard(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 1024, 1449))
	for y := 0; y < 1449; y++ {
		for x := 0; x < 1024; x++ {
			img.Set(x, y, color.RGBA{R: 30, G: 30, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "og.png"), buf.Bytes(), 0644)

	in := `<head><meta property="og:image" content="/og.png"><meta name="twitter:image" content="/og.png"></head>`
	got := ensureOgImageType(in)
	got = ensureOgCard(got, dir)
	got = ensureOgImageDims(got, dir)
	for _, w := range []string{
		`<meta property="og:image" content="/og-card.png">`,
		`<meta property="og:image:width" content="1200">`,
		`<meta property="og:image:height" content="630">`,
		`<meta property="og:image:type" content="image/png">`,
		`<meta name="twitter:image" content="/og-card.png">`,
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in %s", w, got)
		}
	}
	cfg, _, err := image.DecodeConfig(mustOpen(t, filepath.Join(dir, "og-card.png")))
	if err != nil {
		t.Fatalf("decode og-card: %v", err)
	}
	if cfg.Width != 1200 || cfg.Height != 630 {
		t.Errorf("og-card dims = %dx%d, want 1200x630", cfg.Width, cfg.Height)
	}
}

func TestEnsureAppleTouchIcon(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 180, 180))
	var buf bytes.Buffer
	png.Encode(&buf, img)
	os.WriteFile(filepath.Join(dir, "icon-180.png"), buf.Bytes(), 0644)

	in := `<head></head><body></body>`
	got := ensureAppleTouchIcon(in, dir)
	if !strings.Contains(got, `<link rel="apple-touch-icon" sizes="180x180" href="/icon-180.png">`) {
		t.Errorf("apple-touch 180 link not injected: %s", got)
	}
	// idempotent
	if got2 := ensureAppleTouchIcon(got, dir); strings.Count(got2, `sizes="180x180"`) != 1 {
		t.Errorf("should not double-inject: %s", got2)
	}
}

func TestCommonTransformiOSMeta(t *testing.T) {
	job := &Job{Mode: "htmlview", PageTitle: "Frank Ocean Tracker", Lang: "en"}
	in := `<html><head><title>t</title></head><body><img src="/static/x.png"></body></html>`
	got := commonTransform(in, job)
	for _, w := range []string{
		`<meta name="handheldfriendly" content="true">`,
		`<meta name="MobileOptimized" content="width">`,
		`<meta name="twitter:label1" content="Artist">`,
		`<meta name="twitter:data2" content="Released + unreleased + leaks">`,
		`fetchpriority="low"`,
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q", w)
		}
	}
}

func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	return f
}

func TestNoVisibleContentInjected(t *testing.T) {
	job := &Job{Mode: "htmlview", PageTitle: "Frank Ocean Tracker", Lang: "en"}
	html := `<html><head></head><body><div class="sheet"></div></body></html>`
	got := commonTransform(html, job)
	for _, w := range []string{`<footer`, `tracker-about`, `<h1`, `contentinfo`} {
		if strings.Contains(got, w) {
			t.Errorf("unexpected visible content %q present\n%s", w, got)
		}
	}
}

func TestInjectSEOMetaNoSiteURL(t *testing.T) {
	job := &Job{Mode: "htmlview", PageTitle: "Frank Tracker", Lang: "en"}
	html := `<html><head><title>t</title></head><body></body></html>`
	got := injectSEOMeta(html, job)
	if strings.Contains(got, `rel="canonical"`) {
		t.Error("canonical should not be emitted without a site domain")
	}
	if strings.Contains(got, `og:url`) {
		t.Error("og:url should not be emitted without a site domain")
	}
	if !strings.Contains(got, `<html lang="en" dir="ltr">`) {
		t.Error("html lang attribute not injected")
	}
}
