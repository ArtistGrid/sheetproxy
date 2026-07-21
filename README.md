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
      "page_title": "Tracker One"
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

Each sheet runs in its own goroutine, generating into its own `www_dir` and
force-pushing a single squashed commit to its own `git_repo`.

Global JSON settings are overridden by the equivalent environment variables
when both are present.

## Build & Run

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o sheetproxy main.go
./sheetproxy
```