package main

import (
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
