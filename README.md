# sheetproxy

Static mirrors of Google Sheets

## Environment Variables

Shared settings (used by every sheet):

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `GIT_PAT` | no | | GitHub PAT for push (shared across all sheets) |
| `GIT_EMAIL` | no | `sheetproxy@local` | Git commit email |
| `GIT_NAME` | no | `sheetproxy` | Git commit name |
| `POLL_MINUTES` | no | `10` | Default regeneration interval |
| `CONCURRENCY` | no | `8` | Concurrent fetches |
| `CONFIG_FILE` | no | `/srv/sheetproxy/config.json` | Path to a JSON config file (see below) |
| `CWEBP_BIN` | no | `cwebp` | Path to the `cwebp` binary used for WebP encoding |

### Images: lossless WebP

Images are always stored losslessly. Large images and any JPEG source are
re-encoded to **lossless WebP** (falling back to PNG), so the proxy never emits
a lossy JPEG. This requires the `cwebp` binary at runtime (install it locally,
e.g. `apt install webp` / `brew install webp`). Set `CWEBP_BIN` if it is not on
`PATH`.

### Single sheet (backward compatible)

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `SHEET_URL` | yes* | | Google Sheet URL |
| `WWW_DIR` | no | `./www` | Output directory |
| `GIT_REPO` | no | | GitHub repo (e.g. `user/repo`) |
| `PAGE_TITLE` | no | `Frank Tracker` | Page title |
| `PAGE_DESCRIPTION` | no, auto | auto | Meta description (SEO) |
| `ARTIST_NAME` | no, auto | auto | Artist name, derived from `page_title` |
| `PAGE_LANG` | no | `en` | `<html lang>` attribute |
| `FAVICON_URL` | no | | Favicon URL |
| `ANALYTICS` | no | `true` | Enable Plausible analytics |
| `PLAUSIBLE_SCRIPT` | no | | Plausible script URL |

\* `SHEET_URL` is required only when no `CONFIG_FILE` is set (including the
  default path). If `CONFIG_FILE` is explicitly set but the file is missing,
  the process errors out.

### Multiple sheets

Set `CONFIG_FILE` to a JSON file, or rely on the default
`/srv/sheetproxy/config.json` (used automatically when `CONFIG_FILE` is unset).
The top level holds shared/global settings (including the GitHub `git_pat`,
shared by every sheet), and a `jobs` array lists each sheet. Sheets are
identified by `sheet_id` (the ID from the spreadsheet URL), or `sheet_url` as a
fallback. See `config_example.json` for a full example:

```json
{
  "git_pat": "ghp_xxx",
  "git_email": "sheetproxy@local",
  "git_name": "sheetproxy",
  "poll_minutes": 10,
  "concurrency": 8,
  "jobs": [
    {
      "sheet_id": "1A2b3C4d...",
      "www_dir": "./www1",
      "git_repo": "user/repo-one",
      "page_title": "Tracker One",
      "page_description": "Release tracker for Tracker One"
    },
    {
      "sheet_id": "2B3c4D5e...",
      "www_dir": "./www2",
      "git_repo": "user/repo-two",
      "page_title": "Tracker Two",
      "favicon_url": "https://example.com/fav.ico",
      "analytics": false
    }
  ]
}
```

### SEO

Each generated page is injected with auto-generated, domain-free SEO and
AI/answer-engine (GEO) friendly markup. No site domain is required or
hardcoded; everything is derived from the sheet and `page_title`.

- **Basic**: `<title>`, `<html lang>`, meta `description`, `keywords`,
  `author`, `application-name`, `format-detection`, `theme-color`,
  `color-scheme`.
- **Crawler / AI directives**: `robots`, `googlebot` and `bingbot` set to
  `index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1`
  so bots and answer engines can use full snippets and large images.
- **Mobile / PWA**: `apple-mobile-web-app-*`, `mobile-web-app-capable`.
- **Open Graph**: `og:title`, `og:description`, `og:type`, `og:locale`,
  `og:site_name`, and `og:image` (reused from the sheet's own preview image)
  plus `og:image:alt` and `og:image:type`.
- **Twitter Card**: `summary_large_image` with `twitter:title`,
  `twitter:description`, `twitter:image` and `twitter:image:alt`.
- **Structured data**: a JSON-LD `@graph` linking `MusicGroup`, `Person`,
  `WebPage`, `WebSite`, `Organization`, `Dataset`, `BreadcrumbList` and
  `FAQPage` nodes — cross-referenced via `@id` with `member`/`memberOf`,
  `mainEntity`, `isPartOf`, `publisher`, `provider`, `creator`, `about`,
  `subjectOf`, `logo`, `primaryImageOfPage` and `isFamilyFriendly`.
- **No visible changes**: everything is injected into `<head>` or emitted as
  separate files — the rendered page body is untouched.
- **Freshness**: `LastUpdate` is stamped into `og:updated_time`,
  `article:published_time`/`modified_time`, `dcterms.date`/`dcterms.modified`
  and `dateModified` in the structured data on every regenerate.
- **Dublin Core metadata**: `dcterms.title`, `creator`, `subject`,
  `description`, `language`, `type`, `date`, `modified` plus `copyright`.
- **Performance hints**: `preconnect`/`dns-prefetch` for the analytics host,
  `gstatic` and `docs.google.com`.
- **Mobile/OS**: `dir="ltr"`, light/dark `theme-color`,
  `msapplication-TileColor`/`TileImage`, `handheldfriendly`,
  `MobileOptimized`, `apple-mobile-web-app-*` and `mobile-web-app-capable`.
- **Sharing**: a dedicated `og-card.png` (1200x630, cover-cropped from the sheet
  preview) is generated and used for `og:image`/`twitter:image`, with accurate
  `width`/`height`/`type`; `twitter:label1/data1` + `label2/data2`.
- **Images**: sheet images get `loading="lazy"`, `decoding="async"`,
  `fetchpriority="low"`, `crossorigin` and `referrerpolicy="no-referrer"`.
- **AEO / LLM**: `llms.txt` (and a fuller `llms-full.txt`) are generated with
  the site summary and key topics, and `robots.txt` explicitly allows
  AI/answer-engine crawlers (GPTBot, ChatGPT-User, Google-Extended, ClaudeBot,
  anthropic-ai, PerplexityBot, CCBot) plus standard crawlers.

### PWA

Each site is installable as a PWA. A `manifest.webmanifest` is generated with
`id`, `name`, `short_name`, `description`, `start_url`, `scope`, `standalone`
display (with `display_override`), dark `theme_color`/`background_color`,
`lang` and icons: `icon-180.png`/`icon-192.png`/`icon-512.png` plus a
`icon-maskable-512.png` (all resized from the sheet's preview image), the
favicon and the preview image itself. The `<head>` links the manifest and an
`apple-touch-icon` (including a `180x180` variant) and includes
`apple-mobile-web-app-*` / `mobile-web-app-capable` metas so it can be added to
a home screen on iOS and Android.

The artist name is derived from `page_title` (e.g. `Frank Ocean Tracker` →
`Frank Ocean`; override with `artist_name`) and used to generate the
description ("View and explore {artist}'s unreleased and released discography
and music leaks on {title}."), a leak-heavy keywords list (unreleased music,
music leaks, leaks, grails) and the structured data. Set `page_description` to
override the generated description.

A `canonical` link / `og:url` are deliberately not emitted since they require
an absolute site domain.

Each sheet runs in its own goroutine, generating into its own `www_dir` and
force-pushing a single squashed commit to its own `git_repo`.

Global JSON settings are overridden by the equivalent environment variables
when both are present.

## Build & Run

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o sheetproxy main.go
./sheetproxy
```