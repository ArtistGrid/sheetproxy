# sheetproxy

Static mirrors of Google Sheets

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `SHEET_URL` | yes | | Google Sheet URL |
| `WWW_DIR` | no | `./www` | Output directory |
| `POLL_MINUTES` | no | `10` | Regeneration interval |
| `GIT_REPO` | no | | GitHub repo (e.g. `user/repo`) |
| `GIT_PAT` | no | | GitHub PAT for push |

## Build & Run

```bash
go build -o sheetproxy main.go
./sheetproxy
```

## Docker

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o sheetproxy main.go
docker build -t sheetproxy .
docker run --env-file .env sheetproxy
```