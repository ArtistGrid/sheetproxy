package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	he "html"
	"image"
	"image/draw"
	_ "image/gif"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	pollMinutes       = 10
	gitPAT            = ""
	gitEmail          = ""
	gitName           = ""
	concurrency       = 8
	maxBodySize       = int64(100 * 1024 * 1024)
	imgThresholdBytes = 24 * 1024 * 1024
	cwebpBin          = "cwebp"

	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124 Safari/537.36"

	reTelemetry       = regexp.MustCompile(`(?s)<script\b[^>]*>\s*window\['ppConfig'\].*?</script>`)
	reImgTag          = regexp.MustCompile(`(?i)<img\b`)
	rePointerNone     = regexp.MustCompile(`(?i)(<img\b[^>]*style="[^"]*?)pointer-events:\s*none`)
	reSizeSuffix      = regexp.MustCompile(`=w\d+(?:-h\d+)?`)
	reSupportRedirect = regexp.MustCompile(`window\.location\.href\s*=\s*'[^']*support\.google\.com[^']*'`)
	reGoogleRedirect  = regexp.MustCompile(`(href=")https://www\.google\.com/url\?q=([^&"]+)[^"]*(")`)
	reGid             = regexp.MustCompile(`gid=(\d+)`)
	reAssetPath       = regexp.MustCompile(`(?i)(?:src|href)=['"](/(?:static|_|htmlview|preview)/[^'"]+)['"]`)
	reExtImageSheets  = regexp.MustCompile(`https://docs\.google\.com/sheets-images-rt/[A-Za-z0-9_=-]+`)
	reExtImageLH7     = regexp.MustCompile(`https://lh7-us\.googleusercontent\.com/[^\s"'<>)]+`)
	reExtImageStatic  = regexp.MustCompile(`https://ssl\.gstatic\.com/[^\s"'<>)]+`)
	reCssImport       = regexp.MustCompile(`@import url\(((?:https?:)?//[^)]+)\);?`)
	reCssUrl          = regexp.MustCompile(`url\(((?:https?:)?//[^)]+)\)`)
	reKitParam        = regexp.MustCompile(`[?&]kit=`)
	reInlineStyle     = regexp.MustCompile(`(?s)<style>(.*?)</style>`)
	reHasExternal     = regexp.MustCompile(`https?://|url\(`)
	reExtFromPath     = regexp.MustCompile(`\.([a-zA-Z0-9]+)(?:[?#]|$)`)
	reLH7Strip        = regexp.MustCompile(`=w\d+(?:-h\d+)?(?:-p)?$`)
	reTitle           = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	reOgTitle         = regexp.MustCompile(`(?s)<meta\s+property="og:title"\s+content="[^"]*"\s*>`)
	reOgUrl           = regexp.MustCompile(`(?s)<meta\s+property="og:url"\s+content="[^"]*"\s*>`)
	reOgImage         = regexp.MustCompile(`(?s)<meta\s+property="og:image"\s+content="([^"]*)"\s*>`)
	reOgImageType     = regexp.MustCompile(`(?s)<meta\s+property="og:image:type"\s+content="[^"]*"\s*>`)
	reOgImageWidth    = regexp.MustCompile(`(?s)<meta\s+property="og:image:width"\s+content="[^"]*"\s*>`)
	reOgImageHeight   = regexp.MustCompile(`(?s)<meta\s+property="og:image:height"\s+content="[^"]*"\s*>`)
	reTwitterImage    = regexp.MustCompile(`(?s)<meta\s+name="twitter:image"\s+content="[^"]*"\s*>`)
	reOgType          = regexp.MustCompile(`(?s)<meta\s+property="og:type"\s+content="[^"]*"\s*>`)
	reOgSiteName      = regexp.MustCompile(`(?s)<meta\s+property="og:site_name"\s+content="[^"]*"\s*>`)
	reOgDescription   = regexp.MustCompile(`(?s)<meta\s+property="og:description"\s+content="[^"]*"\s*>`)
	reTwitterCard     = regexp.MustCompile(`(?s)<meta\s+name="twitter:card"\s+content="[^"]*"\s*>`)
	reCanonical       = regexp.MustCompile(`(?s)<link\s+rel="canonical"\s+href="[^"]*"\s*>`)
	reMetaDescription = regexp.MustCompile(`(?s)<meta\s+name="description"\s+content="[^"]*"\s*>`)
	reMetaRobots      = regexp.MustCompile(`(?s)<meta\s+name="robots"\s+content="[^"]*"\s*>`)
	reHtmlLang        = regexp.MustCompile(`(?s)<html\b[^>]*>`)
	reDocTitle        = regexp.MustCompile(`(?s)<span\s+class="name">[^<]*</span>`)
	reReferencedAsset = regexp.MustCompile(`(?:src|href)=['"](/assets/[^'"]+)['"]`)
	reSheetUrl        = regexp.MustCompile(`https://docs\.google\.com/spreadsheets/d/[^/"'\''<> ]+`)
	reFavicon         = regexp.MustCompile(`(?i)<link\b[^>]*\brel=["']?(?:shortcut\s+)?icon["']?[^>]*>`)

	client = &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	jobs []*Job
)

type Job struct {
	SheetURL        string
	SheetPath       string
	WwwDir          string
	GitRepo         string
	PageTitle       string
	FaviconURL      string
	FaviconHref     string
	Analytics       bool
	PlausibleScript string
	Lang            string
	PageDescription string
	ArtistName      string
	PollMinutes     int
	Mode            string
	LastUpdate      time.Time

	mu            sync.Mutex
	cssImportSeen sync.Map
	LastHash      string
}

type configFile struct {
	GitPAT      string      `json:"git_pat"`
	GitEmail    string      `json:"git_email"`
	GitName     string      `json:"git_name"`
	PollMinutes int         `json:"poll_minutes"`
	Concurrency int         `json:"concurrency"`
	Mode        string      `json:"mode"`
	Jobs        []jobConfig `json:"jobs"`
}

type jobConfig struct {
	SheetID         string `json:"sheet_id"`
	SheetURL        string `json:"sheet_url"`
	WwwDir          string `json:"www_dir"`
	GitRepo         string `json:"git_repo"`
	PageTitle       string `json:"page_title"`
	FaviconURL      string `json:"favicon_url"`
	Analytics       *bool  `json:"analytics"`
	PlausibleScript string `json:"plausible_script"`
	Lang            string `json:"lang"`
	PageDescription string `json:"page_description"`
	ArtistName      string `json:"artist_name"`
	PollMinutes     int    `json:"poll_minutes"`
	Mode            string `json:"mode"`
}

func normalizeJob(c jobConfig) (*Job, error) {
	u := c.SheetURL
	if c.SheetID != "" {
		u = "https://docs.google.com/spreadsheets/d/" + c.SheetID
	}
	u = strings.TrimRight(u, "/")
	u = strings.TrimSuffix(u, "/htmlview")
	u = strings.TrimSuffix(u, "/preview")
	if u == "" {
		return nil, fmt.Errorf("sheet_id or sheet_url required")
	}
	u2, err := url.Parse(u)
	if err != nil {
		return nil, fmt.Errorf("invalid sheet_url: %w", err)
	}
	wwwDir := strings.TrimRight(c.WwwDir, "/")
	if wwwDir == "" {
		wwwDir = "./www"
	}
	title := c.PageTitle
	if title == "" {
		title = "Frank Tracker"
	}
	lang := c.Lang
	if lang == "" {
		lang = "en"
	}
	artist := c.ArtistName
	if artist == "" {
		artist = artistFromTitle(title)
	}
	pm := c.PollMinutes
	if pm <= 0 {
		pm = pollMinutes
	}
	analytics := true
	if c.Analytics != nil {
		analytics = *c.Analytics
	}
	mode := strings.ToLower(c.Mode)
	if mode == "" {
		mode = "htmlview"
	}
	if mode != "htmlview" && mode != "preview" {
		mode = "htmlview"
	}
	return &Job{
		SheetURL:        u,
		SheetPath:       u2.Path,
		WwwDir:          wwwDir,
		GitRepo:         c.GitRepo,
		PageTitle:       title,
		FaviconURL:      c.FaviconURL,
		Analytics:       analytics,
		PlausibleScript: c.PlausibleScript,
		Lang:            lang,
		PageDescription: c.PageDescription,
		ArtistName:      artist,
		PollMinutes:     pm,
		Mode:            mode,
	}, nil
}

func init() {
	if v := os.Getenv("POLL_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			pollMinutes = n
		}
	}
	if v := os.Getenv("CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			concurrency = n
		}
	}
	gitPAT = os.Getenv("GIT_PAT")
	gitEmail = os.Getenv("GIT_EMAIL")
	gitName = os.Getenv("GIT_NAME")
	if gitEmail == "" {
		gitEmail = "sheetproxy@local"
	}
	if gitName == "" {
		gitName = "sheetproxy"
	}
	if v := os.Getenv("CWEBP_BIN"); v != "" {
		cwebpBin = v
	}

	var cfgs []jobConfig
	configPath := os.Getenv("CONFIG_FILE")
	useDefaultPath := false
	if configPath == "" {
		configPath = "/srv/sheetproxy/config.json"
		useDefaultPath = true
	}
	if _, statErr := os.Stat(configPath); statErr == nil {
		data, err := os.ReadFile(configPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "CONFIG_FILE read error:", err)
			os.Exit(1)
		}
		var cfg configFile
		if err := json.Unmarshal(data, &cfg); err != nil {
			fmt.Fprintln(os.Stderr, "CONFIG_FILE parse error:", err)
			os.Exit(1)
		}
		if cfg.GitPAT != "" {
			gitPAT = cfg.GitPAT
		}
		if cfg.GitEmail != "" {
			gitEmail = cfg.GitEmail
		}
		if cfg.GitName != "" {
			gitName = cfg.GitName
		}
		if cfg.PollMinutes > 0 {
			pollMinutes = cfg.PollMinutes
		}
		if cfg.Concurrency > 0 {
			concurrency = cfg.Concurrency
		}
		cfgs = cfg.Jobs
	} else if useDefaultPath {
		u := strings.TrimRight(os.Getenv("SHEET_URL"), "/")
		u = strings.TrimSuffix(u, "/htmlview")
		u = strings.TrimSuffix(u, "/preview")
		if u == "" {
			fmt.Fprintln(os.Stderr, "SHEET_URL or CONFIG_FILE required")
			os.Exit(1)
		}
		analytics := true
		if v := os.Getenv("ANALYTICS"); v != "" {
			analytics = v == "true" || v == "1"
		}
		cfgs = []jobConfig{{
			SheetURL:        u,
			WwwDir:          os.Getenv("WWW_DIR"),
			GitRepo:         os.Getenv("GIT_REPO"),
			PageTitle:       os.Getenv("PAGE_TITLE"),
			FaviconURL:      os.Getenv("FAVICON_URL"),
			Analytics:       &analytics,
			PlausibleScript: os.Getenv("PLAUSIBLE_SCRIPT"),
			Lang:            os.Getenv("PAGE_LANG"),
			PageDescription: os.Getenv("PAGE_DESCRIPTION"),
			ArtistName:      os.Getenv("ARTIST_NAME"),
			PollMinutes:     pollMinutes,
		}}
	} else {
		fmt.Fprintln(os.Stderr, "CONFIG_FILE read error:", statErr)
		os.Exit(1)
	}

	jobs = make([]*Job, 0, len(cfgs))
	for _, c := range cfgs {
		j, err := normalizeJob(c)
		if err != nil {
			fmt.Fprintln(os.Stderr, "job config error:", err)
			os.Exit(1)
		}
		jobs = append(jobs, j)
	}
}

func sha1hex(s string) string {
	h := sha1.Sum([]byte(s))
	return fmt.Sprintf("%x", h[:10])
}

func extFromContentType(ct string) string {
	ct = strings.ToLower(ct)
	if strings.Contains(ct, "css") {
		return "css"
	}
	if strings.Contains(ct, "svg") {
		return "svg"
	}
	if strings.Contains(ct, "png") {
		return "png"
	}
	if strings.Contains(ct, "jpeg") || strings.Contains(ct, "jpg") {
		return "jpg"
	}
	if strings.Contains(ct, "gif") {
		return "gif"
	}
	if strings.Contains(ct, "webp") {
		return "webp"
	}
	if strings.Contains(ct, "icon") {
		return "ico"
	}
	if strings.Contains(ct, "woff2") {
		return "woff2"
	}
	if strings.Contains(ct, "woff") {
		return "woff"
	}
	if strings.Contains(ct, "ttf") || strings.Contains(ct, "truetype") {
		return "ttf"
	}
	if strings.Contains(ct, "otf") || strings.Contains(ct, "opentype") {
		return "otf"
	}
	return "bin"
}

func extFromFilename(path string) string {
	m := reExtFromPath.FindStringSubmatch(path)
	if len(m) > 1 {
		return m[1]
	}
	return "bin"
}

func commonTransform(html string, job *Job) string {
	if job.Mode == "preview" {
		html = strings.ReplaceAll(html, "/htmlview/", "/preview/")
	} else {
		html = strings.ReplaceAll(html, "/preview/", "/htmlview/")
	}
	html = reTelemetry.ReplaceAllString(html, "")
	html = reImgTag.ReplaceAllString(html, `<img crossorigin="anonymous" referrerpolicy="no-referrer" loading="lazy" decoding="async" fetchpriority="low"`)
	html = rePointerNone.ReplaceAllString(html, `${1}pointer-events:all`)
	html = reSizeSuffix.ReplaceAllString(html, "=w16383")
	html = reSupportRedirect.ReplaceAllString(html, "void 0")
	html = reGoogleRedirect.ReplaceAllStringFunc(html, func(match string) string {
		parts := reGoogleRedirect.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}
		decoded, err := url.QueryUnescape(parts[2])
		if err != nil {
			return match
		}
		return parts[1] + decoded + parts[3]
	})

	html = strings.ReplaceAll(html, `"docs-Helvetica Neue"`, `"Helvetica Neue"`)
	html = reTitle.ReplaceAllLiteralString(html, `<title>`+job.PageTitle+`</title>`)
	html = reOgUrl.ReplaceAllString(html, "")
	html = reSheetUrl.ReplaceAllString(html, "")
	html = reOgTitle.ReplaceAllLiteralString(html, `<meta property="og:title" content="`+job.PageTitle+`">`)
	html = reDocTitle.ReplaceAllLiteralString(html, `<span class="name">`+job.PageTitle+`</span>`)

	if job.FaviconHref != "" {
		html = reFavicon.ReplaceAllLiteralString(html, `<link rel="icon" href="`+job.FaviconHref+`">`)
	}

	if job.Analytics && job.PlausibleScript != "" {
		plausibleTag := `<!-- Privacy-friendly analytics by Plausible -->
<script async src="` + job.PlausibleScript + `"></script>
<script>
  window.plausible=window.plausible||function(){(plausible.q=plausible.q||[]).push(arguments)},plausible.init=plausible.init||function(i){plausible.o=i||{}};
  plausible.init()
</script>`
		if idx := strings.Index(html, "</head>"); idx != -1 {
			html = html[:idx] + plausibleTag + html[idx:]
		}
	}

	html = injectSEOMeta(html, job)
	return html
}

func artistFromTitle(title string) string {
	title = strings.TrimSpace(title)
	suffixes := []string{
		"Tracker", "tracker",
		"Tracker Sheet", "tracker sheet",
		"Discography", "discography",
		"Discography Tracker", "discography tracker",
		"Discogs", "discogs",
		"Database", "database",
	}
	for _, s := range suffixes {
		if strings.HasSuffix(title, " "+s) {
			title = strings.TrimSpace(strings.TrimSuffix(title, " "+s))
		}
	}
	return title
}

func shortName(artist string) string {
	artist = strings.TrimSpace(artist)
	for _, f := range strings.FieldsFunc(artist, func(r rune) bool {
		return r == ',' || r == ' ' || r == '.'
	}) {
		if len(f) >= 3 {
			return f + " Tracker"
		}
	}
	if len(artist) > 12 {
		return artist[:12]
	}
	return artist
}

func ogImageType(path string) string {
	switch {
	case strings.HasSuffix(path, ".png"):
		return "image/png"
	case strings.HasSuffix(path, ".jpg"), strings.HasSuffix(path, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(path, ".webp"):
		return "image/webp"
	case strings.HasSuffix(path, ".gif"):
		return "image/gif"
	case strings.HasSuffix(path, ".svg"):
		return "image/svg+xml"
	}
	return ""
}

func ensureOgImageDims(html, wwwDir string) string {
	m := reOgImage.FindStringSubmatch(html)
	if len(m) < 2 || !strings.HasPrefix(m[1], "/") {
		return html
	}
	f, err := os.Open(filepath.Join(wwwDir, strings.TrimPrefix(m[1], "/")))
	if err != nil {
		return html
	}
	cfg, _, cerr := image.DecodeConfig(f)
	f.Close()
	if cerr != nil || cfg.Width == 0 || cfg.Height == 0 {
		return html
	}
	w := fmt.Sprintf("%d", cfg.Width)
	h := fmt.Sprintf("%d", cfg.Height)
	if reOgImageWidth.MatchString(html) {
		html = reOgImageWidth.ReplaceAllString(html, `<meta property="og:image:width" content="`+w+`">`)
	} else {
		tag := reOgImage.FindString(html)
		html = strings.Replace(html, tag, tag+`<meta property="og:image:width" content="`+w+`">`, 1)
	}
	if reOgImageHeight.MatchString(html) {
		html = reOgImageHeight.ReplaceAllString(html, `<meta property="og:image:height" content="`+h+`">`)
	} else {
		tag := reOgImageWidth.FindString(html)
		html = strings.Replace(html, tag, tag+`<meta property="og:image:height" content="`+h+`">`, 1)
	}
	return html
}

func encodeResizedPNG(src image.Image, size int) ([]byte, bool) {
	b := src.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	if srcW == 0 || srcH == 0 || size <= 0 {
		return nil, false
	}
	side := srcW
	if srcH < side {
		side = srcH
	}
	dx, dy := (srcW-side)/2, (srcH-side)/2
	crop := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		sy := dy + (y * side / size)
		for x := 0; x < size; x++ {
			crop.Set(x, y, src.At(dx+(x*side/size), sy))
		}
	}
	var buf bytes.Buffer
	if png.Encode(&buf, crop) != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

func pwaIcons(job *Job, ogImage string) []map[string]string {
	var icons []map[string]string
	appendRef := func(src, sizes, purpose string) {
		ic := map[string]string{"src": src, "sizes": sizes, "purpose": purpose}
		if t := ogImageType(src); t != "" {
			ic["type"] = t
		}
		icons = append(icons, ic)
	}

	if job.FaviconHref != "" {
		appendRef(job.FaviconHref, "any", "any")
	}
	if ogImage == "" {
		return icons
	}

	var img image.Image
	if f, err := os.Open(filepath.Join(job.WwwDir, strings.TrimPrefix(ogImage, "/"))); err == nil {
		if dec, _, derr := image.Decode(f); derr == nil {
			img = dec
		}
		f.Close()
	}

	if img != nil {
		for _, s := range []int{180, 192, 512} {
			if data, ok := encodeResizedPNG(img, s); ok {
				name := fmt.Sprintf("icon-%d.png", s)
				writeFile(filepath.Join(job.WwwDir, name), data)
				appendRef("/"+name, fmt.Sprintf("%dx%d", s, s), "any")
			}
		}
		if data, ok := encodeResizedPNG(img, 512); ok {
			writeFile(filepath.Join(job.WwwDir, "icon-maskable-512.png"), data)
			appendRef("/icon-maskable-512.png", "512x512", "maskable")
		}
	}

	appendRef(ogImage, "1200x630", "any")
	return icons
}

func encodeCoverPNG(src image.Image, w, h int) ([]byte, bool) {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw == 0 || sh == 0 || w <= 0 || h <= 0 {
		return nil, false
	}
	scale := math.Max(float64(w)/float64(sw), float64(h)/float64(sh))
	tw := int(math.Round(float64(sw) * scale))
	th := int(math.Round(float64(sh) * scale))
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy := int(float64(y) * float64(th) / float64(h))
		for x := 0; x < w; x++ {
			sx := int(float64(x) * float64(tw) / float64(w))
			if sx < sw && sy < sh {
				out.Set(x, y, src.At(sx, sy))
			}
		}
	}
	var buf bytes.Buffer
	if png.Encode(&buf, out) != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

func ensureOgCard(html, wwwDir string) string {
	m := reOgImage.FindStringSubmatch(html)
	if len(m) < 2 || !strings.HasPrefix(m[1], "/") {
		return html
	}
	f, err := os.Open(filepath.Join(wwwDir, strings.TrimPrefix(m[1], "/")))
	if err != nil {
		return html
	}
	img, _, derr := image.Decode(f)
	f.Close()
	if derr != nil {
		return html
	}
	data, ok := encodeCoverPNG(img, 1200, 630)
	if !ok {
		return html
	}
	writeFile(filepath.Join(wwwDir, "og-card.png"), data)
	html = reOgImage.ReplaceAllString(html, `<meta property="og:image" content="/og-card.png">`)
	html = reOgImageType.ReplaceAllString(html, `<meta property="og:image:type" content="image/png">`)
	html = reOgImageWidth.ReplaceAllString(html, `<meta property="og:image:width" content="1200">`)
	html = reOgImageHeight.ReplaceAllString(html, `<meta property="og:image:height" content="630">`)
	if reTwitterImage.MatchString(html) {
		html = reTwitterImage.ReplaceAllString(html, `<meta name="twitter:image" content="/og-card.png">`)
	}
	return html
}

func ensureAppleTouchIcon(html, wwwDir string) string {
	if _, err := os.Stat(filepath.Join(wwwDir, "icon-180.png")); err != nil {
		return html
	}
	if strings.Contains(html, `apple-touch-icon" sizes="180x180"`) {
		return html
	}
	link := `<link rel="apple-touch-icon" sizes="180x180" href="/icon-180.png">`
	if idx := strings.Index(html, "</head>"); idx != -1 {
		html = html[:idx] + link + "\n" + html[idx:]
	}
	return html
}

func ensureOgImageType(html string) string {
	img := reOgImage.FindStringSubmatch(html)
	if len(img) < 2 {
		return html
	}
	t := ogImageType(img[1])
	if t == "" {
		return html
	}
	if reOgImageType.MatchString(html) {
		return reOgImageType.ReplaceAllString(html, `<meta property="og:image:type" content="`+t+`">`)
	}
	tag := reOgImage.FindString(html)
	return strings.Replace(html, tag, tag+`<meta property="og:image:type" content="`+t+`">`, 1)
}

func injectSEOMeta(html string, job *Job) string {
	artist, desc, escTitle, escDesc, _ := pageSEO(job)
	escArtist := he.EscapeString(artist)

	ogImage := ""
	if m := reOgImage.FindStringSubmatch(html); len(m) > 1 {
		ogImage = m[1]
	}

	var b strings.Builder
	b.WriteString(`<meta name="description" content="` + escDesc + `">` + "\n")
	b.WriteString(`<meta name="robots" content="index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1">` + "\n")
	b.WriteString(`<meta name="googlebot" content="index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1">` + "\n")
	b.WriteString(`<meta name="bingbot" content="index, follow, max-image-preview:large, max-snippet:-1">` + "\n")
	b.WriteString(`	<link rel="manifest" href="/manifest.webmanifest">` + "\n")
	if ogImage != "" {
		b.WriteString(`<link rel="apple-touch-icon" href="` + he.EscapeString(ogImage) + `">` + "\n")
	}
	if job.PlausibleScript != "" {
		if pu, perr := url.Parse(job.PlausibleScript); perr == nil && pu.Host != "" {
			b.WriteString(`<link rel="preconnect" href="https://` + pu.Host + `">` + "\n")
			b.WriteString(`<link rel="dns-prefetch" href="https://` + pu.Host + `">` + "\n")
		}
	}
	b.WriteString(`<link rel="dns-prefetch" href="https://www.gstatic.com">` + "\n")
	b.WriteString(`<link rel="dns-prefetch" href="https://docs.google.com">` + "\n")
	b.WriteString(`<meta name="author" content="` + escTitle + `">` + "\n")
	b.WriteString(`<meta name="application-name" content="` + escTitle + `">` + "\n")
	b.WriteString(`<meta name="theme-color" content="#111111" media="(prefers-color-scheme: dark)">` + "\n")
	b.WriteString(`<meta name="theme-color" content="#ffffff" media="(prefers-color-scheme: light)">` + "\n")
	b.WriteString(`<meta name="color-scheme" content="light dark">` + "\n")
	b.WriteString(`<meta name="format-detection" content="telephone=no">` + "\n")
	b.WriteString(`<meta name="msapplication-TileColor" content="#111111">` + "\n")
	if ogImage != "" {
		b.WriteString(`<meta name="msapplication-TileImage" content="` + he.EscapeString(ogImage) + `">` + "\n")
	}
	b.WriteString(`<meta name="apple-mobile-web-app-capable" content="yes">` + "\n")
	b.WriteString(`<meta name="apple-mobile-web-app-title" content="` + escTitle + `">` + "\n")
	b.WriteString(`<meta name="apple-mobile-web-app-status-bar-style" content="default">` + "\n")
	b.WriteString(`<meta name="mobile-web-app-capable" content="yes">` + "\n")
	b.WriteString(`<meta name="handheldfriendly" content="true">` + "\n")
	b.WriteString(`<meta name="MobileOptimized" content="width">` + "\n")
	keywords := strings.Join([]string{
		artist + " tracker",
		artist + " discography",
		artist + " unreleased discography",
		artist + " unreleased music",
		artist + " music leaks",
		artist + " leaks",
		artist + " grails",
		artist + " new music",
		"unreleased discography",
		"discography tracker",
		"music leaks",
		"leaks",
	}, ", ")
	b.WriteString(`<meta name="keywords" content="` + he.EscapeString(keywords) + `">` + "\n")
	dctermsDate := ""
	if !job.LastUpdate.IsZero() {
		dctermsDate = job.LastUpdate.Format(time.RFC3339)
	}
	b.WriteString(`<meta name="dcterms.title" content="` + escTitle + `">` + "\n")
	b.WriteString(`<meta name="dcterms.creator" content="` + escTitle + `">` + "\n")
	b.WriteString(`<meta name="dcterms.subject" content="` + he.EscapeString(keywords) + `">` + "\n")
	b.WriteString(`<meta name="dcterms.description" content="` + escDesc + `">` + "\n")
	b.WriteString(`<meta name="dcterms.language" content="` + job.Lang + `">` + "\n")
	b.WriteString(`<meta name="dcterms.type" content="Text">` + "\n")
	if dctermsDate != "" {
		b.WriteString(`<meta name="dcterms.date" content="` + dctermsDate + `">` + "\n")
		b.WriteString(`<meta name="dcterms.modified" content="` + dctermsDate + `">` + "\n")
	}
	b.WriteString(`<meta property="og:type" content="website">` + "\n")
	b.WriteString(`<meta property="og:locale" content="en_US">` + "\n")
	b.WriteString(`<meta property="og:site_name" content="` + escTitle + `">` + "\n")
	b.WriteString(`<meta property="og:title" content="` + escTitle + `">` + "\n")
	b.WriteString(`<meta property="og:description" content="` + escDesc + `">` + "\n")
	if !job.LastUpdate.IsZero() {
		ts := job.LastUpdate.Format(time.RFC3339)
		b.WriteString(`<meta property="og:updated_time" content="` + ts + `">` + "\n")
		b.WriteString(`<meta property="article:modified_time" content="` + ts + `">` + "\n")
	}
	if ogImage != "" {
		b.WriteString(`<meta property="og:image" content="` + he.EscapeString(ogImage) + `">` + "\n")
		b.WriteString(`<meta property="og:image:alt" content="` + escTitle + `">` + "\n")
		if t := ogImageType(ogImage); t != "" {
			b.WriteString(`<meta property="og:image:type" content="` + t + `">` + "\n")
		}
		b.WriteString(`<meta name="twitter:card" content="summary_large_image">` + "\n")
		b.WriteString(`<meta name="twitter:image" content="` + he.EscapeString(ogImage) + `">` + "\n")
		b.WriteString(`<meta name="twitter:image:alt" content="` + escTitle + `">` + "\n")
	} else {
		b.WriteString(`<meta name="twitter:card" content="summary">` + "\n")
	}
	b.WriteString(`<meta name="twitter:title" content="` + escTitle + `">` + "\n")
	b.WriteString(`<meta name="twitter:description" content="` + escDesc + `">` + "\n")
	b.WriteString(`<meta name="twitter:label1" content="Artist">` + "\n")
	b.WriteString(`<meta name="twitter:data1" content="` + escArtist + `">` + "\n")
	b.WriteString(`<meta name="twitter:label2" content="Covers">` + "\n")
	b.WriteString(`<meta name="twitter:data2" content="Released + unreleased + leaks">` + "\n")

	knowsTopics := []string{
		artist + " discography",
		artist + " unreleased music",
		artist + " music leaks",
		"released and unreleased music",
		"tracking leaks",
	}
	var kw []string
	for _, k := range knowsTopics {
		kw = append(kw, strconv.Quote(k))
	}
	jsonArtist := strconv.Quote(escArtist)
	jsonTitle := strconv.Quote(escTitle)
	jsonDesc := strconv.Quote(escDesc)
	jsonKnows := "[" + strings.Join(kw, ",") + "]"
	jsonImgRef := ""
	if ogImage != "" {
		jsonImgRef = `,"image":` + strconv.Quote(ogImage)
	}
	jsonImgObj := ""
	if ogImage != "" {
		jsonImgObj = `,"primaryImageOfPage":{"@type":"ImageObject","contentUrl":` + strconv.Quote(ogImage) + `}`
	}
	jsonLogoObj := ""
	if ogImage != "" {
		jsonLogoObj = `,"logo":{"@type":"ImageObject","contentUrl":` + strconv.Quote(ogImage) + `}`
	}
	jsonDate := ""
	if !job.LastUpdate.IsZero() {
		jsonDate = `,"dateModified":` + strconv.Quote(job.LastUpdate.Format(time.RFC3339))
	}
	lang := job.Lang
	if lang == "" {
		lang = "en"
	}

	var jd strings.Builder
	jd.WriteString(`{"@context":"https://schema.org","@graph":[`)

	// Artist
	jd.WriteString(`{"@type":"MusicGroup","@id":"#artist","name":` + jsonArtist + `,"alternateName":` + jsonTitle + `,"description":` + jsonDesc + `,"knowsAbout":` + jsonKnows + jsonImgRef + `,"inLanguage":` + strconv.Quote(lang) + `,"member":{"@id":"#person"},"subjectOf":{"@id":"#webpage"}}`)

	// Person (the artist as a person)
	jd.WriteString(`,{"@type":"Person","@id":"#person","name":` + jsonArtist + `,"alternateName":` + jsonTitle + `,"description":` + jsonDesc + `,"knowsAbout":` + jsonKnows + `,"memberOf":{"@id":"#artist"}}`)

	// WebPage (main content, publisher organization)
	jd.WriteString(`,{"@type":"WebPage","@id":"#webpage","name":` + jsonTitle +
		`,"description":` + jsonDesc + `,"about":` + jsonKnows +
		`,"inLanguage":` + strconv.Quote(lang) + jsonDate +
		`,"isFamilyFriendly":true,"mainEntity":{"@id":"#artist"},"isPartOf":{"@id":"#website"},"publisher":{"@id":"#org"}` +
		jsonImgObj + `}`)

	// WebSite
	jd.WriteString(`,{"@type":"WebSite","@id":"#website","name":` + jsonTitle + `,"alternateName":` + jsonArtist + `,"description":` + jsonDesc + `,"inLanguage":` + strconv.Quote(lang) + jsonDate + `,"publisher":{"@id":"#org"}}`)

	// Organization (publisher)
	jd.WriteString(`,{"@type":"Organization","@id":"#org","name":` + jsonTitle + `,"description":` + jsonDesc + `,"inLanguage":` + strconv.Quote(lang) + `,"knowsAbout":` + jsonKnows + jsonLogoObj + `}`)

	// Dataset (the tracker content as a dataset)
	jd.WriteString(`,{"@type":"Dataset","name":` + jsonTitle + `,"description":` + jsonDesc + `,"creator":{"@id":"#artist"},"provider":{"@id":"#org"},"about":` + jsonKnows + `,"inLanguage":` + strconv.Quote(lang) + jsonDate + `,"keywords":` + strconv.Quote(strings.Join([]string{artist + " discography", "unreleased music", "music leaks"}, ", ")) + `}`)

	// Breadcrumbs
	jd.WriteString(`,{"@type":"BreadcrumbList","itemListElement":[{"@type":"ListItem","position":1,"name":` + jsonTitle + `}]}`)

	// FAQPage
	faqQ1 := "What is the " + job.PageTitle + "?"
	faqA1 := desc
	faqQ2 := "Where can I find " + artist + "'s unreleased music and leaks?"
	faqA2 := "The " + job.PageTitle + " collects and tracks " + artist + "'s unreleased and released discography, including unreleased music and leaks, in one place."
	faqQ3 := "Does the " + job.PageTitle + " cover released music too?"
	faqA3 := "Yes — the tracker covers both released albums and singles and unreleased music and leaks."
	faqQ4 := "How often is the " + job.PageTitle + " updated?"
	faqA4 := "The tracker updates automatically, so new releases, leaks and news appear shortly after they happen."
	jd.WriteString(`,{"@type":"FAQPage","@id":"#faq","mainEntity":[{"@type":"Question","name":` + strconv.Quote(faqQ1) + `,"acceptedAnswer":{"@type":"Answer","text":` + strconv.Quote(faqA1) + `}},`)
	jd.WriteString(`{"@type":"Question","name":` + strconv.Quote(faqQ2) + `,"acceptedAnswer":{"@type":"Answer","text":` + strconv.Quote(faqA2) + `}},`)
	jd.WriteString(`{"@type":"Question","name":` + strconv.Quote(faqQ3) + `,"acceptedAnswer":{"@type":"Answer","text":` + strconv.Quote(faqA3) + `}},`)
	jd.WriteString(`{"@type":"Question","name":` + strconv.Quote(faqQ4) + `,"acceptedAnswer":{"@type":"Answer","text":` + strconv.Quote(faqA4) + `}}]}`)

	jd.WriteString(`]}`)
	b.WriteString(`<script type="application/ld+json">` + jd.String() + `</script>` + "\n")

	html = reOgImage.ReplaceAllString(html, "")
	html = reOgType.ReplaceAllString(html, "")
	html = reOgSiteName.ReplaceAllString(html, "")
	html = reOgDescription.ReplaceAllString(html, "")
	html = reOgTitle.ReplaceAllString(html, "")
	html = reOgUrl.ReplaceAllString(html, "")
	html = reTwitterCard.ReplaceAllString(html, "")
	html = reCanonical.ReplaceAllString(html, "")
	html = reMetaDescription.ReplaceAllString(html, "")
	html = reMetaRobots.ReplaceAllString(html, "")
	html = reHtmlLang.ReplaceAllStringFunc(html, func(match string) string {
		if strings.Contains(match, "lang=") {
			return match
		}
		return strings.TrimSuffix(match, ">") + ` lang="` + job.Lang + `" dir="ltr">`
	})

	if idx := strings.Index(html, "</head>"); idx != -1 {
		html = html[:idx] + b.String() + html[idx:]
	}
	return html
}

func extractExternalImageUrls(html string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, re := range []*regexp.Regexp{reExtImageSheets, reExtImageLH7, reExtImageStatic} {
		for _, m := range re.FindAllString(html, -1) {
			if !seen[m] {
				seen[m] = true
				result = append(result, m)
			}
		}
	}
	return result
}

func extractGids(html string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, m := range reGid.FindAllStringSubmatch(html, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			result = append(result, m[1])
		}
	}
	return result
}

func extractAssetPaths(html string, job *Job) []string {
	seen := make(map[string]bool)
	var result []string
	for _, m := range reAssetPath.FindAllStringSubmatch(html, -1) {
		p := m[1]
		if !strings.HasPrefix(p, "/"+job.Mode+"/") && !strings.HasPrefix(p, "/static/") && !strings.HasPrefix(p, "/_/") {
			continue
		}
		if !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	return result
}

func httpGet(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	return client.Do(req)
}

func httpGetRetry(ctx context.Context, rawURL string, maxRetries int) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<uint(attempt-1)) * time.Second):
			}
		}
		resp, err := httpGet(ctx, rawURL)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == 408 || resp.StatusCode == 429 ||
			resp.StatusCode == 500 || resp.StatusCode == 502 ||
			resp.StatusCode == 503 || resp.StatusCode == 504 {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

func fetchFavicon(job *Job) {
	if job.FaviconURL == "" {
		return
	}
	fmt.Println("  fetching favicon...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := httpGetRetry(ctx, job.FaviconURL, 2)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  favicon fetch error: %v\n", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "  favicon fetch: HTTP %d\n", resp.StatusCode)
		return
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		fmt.Fprintf(os.Stderr, "  favicon read error: %v\n", err)
		return
	}

	ext := extFromFilename(job.FaviconURL)
	if ext == "bin" {
		ext = "ico"
	}

	dest := filepath.Join(job.WwwDir, "favicon."+ext)
	writeFile(dest, data)
	job.FaviconHref = "/favicon." + ext
	fmt.Println("  favicon saved as", dest)
}

func mkdirAll(path string) {
	if err := os.MkdirAll(path, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "  mkdir %s: %v\n", path, err)
	}
}

func writeFile(path string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "  mkdir %s: %v\n", path, err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "  write %s: %v\n", path, err)
	}
}

func encodeLosslessWebp(img image.Image) ([]byte, bool) {
	tmpPng, err := os.CreateTemp("", "sheetproxy-*.png")
	if err != nil {
		return nil, false
	}
	tmpPngName := tmpPng.Name()
	defer os.Remove(tmpPngName)
	if err := png.Encode(tmpPng, img); err != nil {
		tmpPng.Close()
		return nil, false
	}
	tmpPng.Close()

	outWebp, err := os.CreateTemp("", "sheetproxy-*.webp")
	if err != nil {
		return nil, false
	}
	outWebpName := outWebp.Name()
	outWebp.Close()
	defer os.Remove(outWebpName)

	cmd := exec.Command(cwebpBin, "-lossless", "-q", "100", tmpPngName, "-o", outWebpName)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := cmd.Run(); err != nil {
		return nil, false
	}
	data, err := os.ReadFile(outWebpName)
	if err != nil {
		return nil, false
	}
	return data, true
}

func localizeImage(rawURL string, job *Job) string {
	name := sha1hex(rawURL)

	for _, ext := range []string{"webp", "png", "gif", "jpg", "ico", "bin"} {
		dest := filepath.Join(job.WwwDir, "assets", name+"."+ext)
		if _, err := os.Stat(dest); err == nil {
			return "/assets/" + name + "." + ext
		}
	}

	fetchURL := rawURL
	if strings.Contains(rawURL, "lh7-us.googleusercontent.com") {
		fetchURL = reLH7Strip.ReplaceAllString(rawURL, "")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	resp, err := httpGetRetry(ctx, fetchURL, 3)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "  image %s: HTTP %d\n", rawURL, resp.StatusCode)
		return ""
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return ""
	}

	ct := resp.Header.Get("Content-Type")
	ext := extFromContentType(ct)

	chHex := fmt.Sprintf("%x", sha256.Sum256(data))
	assetExists := func(e string) (string, bool) {
		dest := filepath.Join(job.WwwDir, "assets", chHex+"."+e)
		if _, err := os.Stat(dest); err == nil {
			return "/assets/" + chHex + "." + e, true
		}
		return "", false
	}

	if p, ok := assetExists("webp"); ok {
		return p
	}
	if p, ok := assetExists(ext); ok {
		return p
	}
	if p, ok := assetExists("png"); ok {
		return p
	}

	if len(data) <= imgThresholdBytes {
		dest := filepath.Join(job.WwwDir, "assets", chHex+"."+ext)
		writeFile(dest, data)
		return "/assets/" + chHex + "." + ext
	}

	if cfg, _, decErr := image.DecodeConfig(bytes.NewReader(data)); decErr == nil && cfg.Width*cfg.Height > 16384*16384 {
		fmt.Fprintf(os.Stderr, "  skipping too-large image %s (%dx%d)\n", rawURL, cfg.Width, cfg.Height)
		return ""
	}

	img, _, decErr := image.Decode(bytes.NewReader(data))
	if decErr != nil {
		dest := filepath.Join(job.WwwDir, "assets", chHex+"."+ext)
		writeFile(dest, data)
		return "/assets/" + chHex + "." + ext
	}
	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)

	if webpData, ok := encodeLosslessWebp(rgba); ok && len(webpData) < imgThresholdBytes {
		dest := filepath.Join(job.WwwDir, "assets", chHex+".webp")
		writeFile(dest, webpData)
		return "/assets/" + chHex + ".webp"
	}

	var pngBuf bytes.Buffer
	if png.Encode(&pngBuf, rgba) == nil && pngBuf.Len() < imgThresholdBytes {
		dest := filepath.Join(job.WwwDir, "assets", chHex+".png")
		writeFile(dest, pngBuf.Bytes())
		return "/assets/" + chHex + ".png"
	}

	dest := filepath.Join(job.WwwDir, "assets", chHex+"."+ext)
	writeFile(dest, data)
	return "/assets/" + chHex + "." + ext
}

func fetchTransformedMain(job *Job) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := httpGetRetry(ctx, job.SheetURL+"/"+job.Mode, 2)
	if err != nil {
		fmt.Println("  main fetch error:", err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Printf("  %d - main fetch failed, cache preserved\n", resp.StatusCode)
		return ""
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return ""
	}
	html := string(data)

	plain := "https://docs.google.com" + job.SheetPath
	escaped := strings.ReplaceAll("https:\\/\\/docs.google.com"+strings.ReplaceAll(job.SheetPath, "/", "\\/"), "/", "\\/")
	html = strings.ReplaceAll(html, plain, "")
	html = strings.ReplaceAll(html, escaped, "")

	return commonTransform(html, job)
}

func fetchTransformedTab(gid string, job *Job) string {
	target := fmt.Sprintf("https://docs.google.com%s/%s/sheet?headers=true&gid=%s", job.SheetPath, job.Mode, gid)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := httpGetRetry(ctx, target, 2)
	if err != nil {
		fmt.Printf("  tab %s fetch error: %v\n", gid, err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Printf("  %d - tab %s fetch failed\n", resp.StatusCode, gid)
		return ""
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return ""
	}
	return commonTransform(string(data), job)
}

func localizeCssAsset(rawURL string, job *Job) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	ext := extFromFilename(u.Path)
	name := sha1hex(rawURL)
	dest := filepath.Join(job.WwwDir, "assets", "css", name+"."+ext)
	if _, err := os.Stat(dest); err == nil {
		return "/assets/css/" + name + "." + ext
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := httpGetRetry(ctx, rawURL, 2)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return ""
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		if mapped := extFromContentType(ct); mapped != "bin" {
			ext = mapped
			dest = filepath.Join(job.WwwDir, "assets", "css", name+"."+ext)
		}
	}
	writeFile(dest, data)
	fmt.Println("  css asset", rawURL)
	return "/assets/css/" + name + "." + ext
}

func localizeCss(css string, job *Job, depth int) string {
	if depth < 3 {
		for _, m := range reCssImport.FindAllStringSubmatch(css, -1) {
			importURL := m[1]
			if strings.HasPrefix(importURL, "//") {
				importURL = "https:" + importURL
			}
			if reKitParam.MatchString(importURL) {
				continue
			}
			if _, loaded := job.cssImportSeen.LoadOrStore(importURL, true); loaded {
				css = strings.Replace(css, m[0], "", 1)
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			resp, err := httpGetRetry(ctx, importURL, 2)
			cancel()
			if err != nil || resp.StatusCode != 200 {
				if resp != nil {
					resp.Body.Close()
				}
				continue
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
			resp.Body.Close()
			if err != nil {
				continue
			}
			localized := localizeCss(string(body), job, depth+1)
			css = strings.Replace(css, m[0], localized, 1)
		}
	}

	seen := make(map[string]bool)
	var urls []string
	for _, m := range reCssUrl.FindAllStringSubmatch(css, -1) {
		u := strings.Trim(m[1], "'\"")
		if !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}

	for _, u := range urls {
		fetchURL := u
		if strings.HasPrefix(u, "//") {
			fetchURL = "https:" + u
		}
		local := localizeCssAsset(fetchURL, job)
		if local != "" {
			css = strings.ReplaceAll(css, u, local)
		}
	}
	return css
}

func localizeInlineStyles(html string, job *Job) string {
	for _, m := range reInlineStyle.FindAllStringSubmatch(html, -1) {
		if !reHasExternal.MatchString(m[1]) {
			continue
		}
		localized := localizeCss(m[1], job, 0)
		if localized != m[1] {
			html = strings.Replace(html, m[0], "<style>"+localized+"</style>", 1)
		}
	}
	return html
}

func downloadAsset(path string, job *Job) {
	dest := filepath.Join(job.WwwDir, path)
	if _, err := os.Stat(dest); err == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := httpGetRetry(ctx, "https://docs.google.com"+path, 2)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "  asset %s: HTTP %d\n", path, resp.StatusCode)
		return
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return
	}

	if strings.HasSuffix(path, ".css") {
		css := localizeCss(string(data), job, 0)
		writeFile(dest, []byte(css))
	} else {
		writeFile(dest, data)
	}
	fmt.Println("  asset", path)
}

func pAll(fns []func(), concurrency int) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for _, fn := range fns {
		wg.Add(1)
		sem <- struct{}{}
		go func(f func()) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "  panic in worker: %v\n", r)
				}
			}()
			f()
		}(fn)
	}
	wg.Wait()
}

type tabResult struct {
	gid  string
	html string
}

func gitPush(ctx context.Context, job *Job) {
	if job.GitRepo == "" || gitPAT == "" {
		fmt.Println("  git push skipped (GIT_REPO/GIT_PAT not set)")
		return
	}

	remoteURL := fmt.Sprintf("https://%s@github.com/%s.git", gitPAT, job.GitRepo)

	gitDir := filepath.Join(job.WwwDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		runGitCtx(ctx, job.WwwDir, "init", "-b", "main")
		runGitCtx(ctx, job.WwwDir, "config", "user.email", gitEmail)
		runGitCtx(ctx, job.WwwDir, "config", "user.name", gitName)
		runGitCtx(ctx, job.WwwDir, "remote", "add", "origin", remoteURL)
	} else {
		runGitCtx(ctx, job.WwwDir, "remote", "set-url", "origin", remoteURL)
	}

	runGitCtx(ctx, job.WwwDir, "rm", "-r", "--cached", "functions")
	runGitCtx(ctx, job.WwwDir, "add", "-A")
	if out, err := runGitCtx(ctx, job.WwwDir, "diff", "--cached", "--quiet"); err == nil && out == "" {
		fmt.Println("  no changes to commit")
		return
	}
	commitMsg := fmt.Sprintf("update %s", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))

	if _, err := runGitCtx(ctx, job.WwwDir, "rev-parse", "HEAD"); err == nil {

		if out, err := runGitCtx(ctx, job.WwwDir, "commit", "--amend", "-m", commitMsg); err != nil {
			fmt.Printf("  git commit: %s\n", out)
		}
	} else {
		if out, err := runGitCtx(ctx, job.WwwDir, "commit", "-m", commitMsg); err != nil {
			fmt.Printf("  git commit: %s\n", out)
		}
	}

	runGitCtx(ctx, job.WwwDir, "reflog", "expire", "--expire=now", "--all")
	runGitCtx(ctx, job.WwwDir, "gc", "--prune=now")

	if out, err := runGitCtx(ctx, job.WwwDir, "push", "--force", "origin", "main"); err != nil {
		fmt.Printf("  git push: %s\n", out)
	} else {
		fmt.Println("  git push ok")
	}
}

func runGitCtx(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

var reAssetRef = regexp.MustCompile(`/assets/[^'") ?#]+`)

func collectReferencedAssets(wwwDir, mainHTML string, tabs []tabResult) map[string]bool {
	ref := make(map[string]bool)
	scan := func(s string) {
		for _, m := range reAssetRef.FindAllString(s, -1) {
			ref[m] = true
		}
	}
	scan(mainHTML)
	for _, t := range tabs {
		scan(t.html)
	}

	queue := []string{}
	for p := range ref {
		if strings.HasPrefix(p, "/assets/css/") {
			queue = append(queue, p)
		}
	}
	visited := make(map[string]bool)
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if visited[p] {
			continue
		}
		visited[p] = true
		data, err := os.ReadFile(filepath.Join(wwwDir, p))
		if err != nil {
			continue
		}
		scan(string(data))
		for p2 := range ref {
			if strings.HasPrefix(p2, "/assets/css/") && !visited[p2] {
				queue = append(queue, p2)
			}
		}
	}
	return ref
}

func cleanupStaleAssets(referenced map[string]bool, wwwDir string) {
	assetDirs := []string{
		filepath.Join(wwwDir, "assets"),
		filepath.Join(wwwDir, "assets", "css"),
	}
	for _, dir := range assetDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			relPath := "/assets/" + filepath.Base(e.Name())
			if dir == filepath.Join(wwwDir, "assets", "css") {
				relPath = "/assets/css/" + filepath.Base(e.Name())
			}
			if !referenced[relPath] {
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
}

func pageSEO(job *Job) (artist, desc, escTitle, escDesc, short string) {
	artist = strings.TrimSpace(job.ArtistName)
	if artist == "" {
		artist = artistFromTitle(job.PageTitle)
	}
	if artist == "" {
		artist = job.PageTitle
	}
	desc = job.PageDescription
	if desc == "" {
		desc = "View and explore " + artist + "'s unreleased and released discography and music leaks on " + job.PageTitle + "."
	}
	escTitle = he.EscapeString(job.PageTitle)
	escDesc = he.EscapeString(desc)
	short = shortName(artist)
	return artist, desc, escTitle, escDesc, short
}

func writePWAOutputs(job *Job, ogImage string) {
	artist, desc, _, _, short := pageSEO(job)

	icons := pwaIcons(job, ogImage)

	manifest := map[string]interface{}{
		"id":               "./",
		"name":             job.PageTitle,
		"short_name":       short,
		"description":      desc,
		"start_url":        "./",
		"scope":            "./",
		"display":          "standalone",
		"display_override": []string{"standalone", "minimal-ui"},
		"orientation":      "any",
		"background_color": "#111111",
		"theme_color":      "#111111",
		"lang":             job.Lang,
		"icons":            icons,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err == nil {
		writeFile(filepath.Join(job.WwwDir, "manifest.webmanifest"), data)
	}

	llms := "# " + job.PageTitle + "\n\n" +
		"> " + desc + "\n\n" +
		artist + " is the subject of this tracker. Here you can explore and stay up to date on the full discography — released and unreleased music, leaks, news and releases — all in one place.\n\n" +
		"## Key topics\n\n" +
		"- " + artist + " discography\n" +
		"- " + artist + " unreleased music\n" +
		"- " + artist + " music leaks\n" +
		"- " + artist + " news and releases\n" +
		"- tracking unreleased and leaked music\n\n" +
		"## About\n\n" +
		"The " + job.PageTitle + " is a curated, community-maintained tracker of " + artist + "'s music catalog. It covers released albums and singles as well as unreleased music, demos, snippets and leaks, updated regularly.\n\n" +
		"## Optional\n\n" +
		"Use this site to find out what " + artist + " has released, what is unreleased or leaked, and to follow news about new music."
	writeFile(filepath.Join(job.WwwDir, "llms.txt"), []byte(llms))

	llmsFull := "# " + job.PageTitle + " (full)\n\n" +
		"> " + desc + "\n\n" +
		"## About\n\n" +
		artist + " is the subject of this tracker. The " + job.PageTitle + " documents the complete music catalog of " + artist + " in a single, community-maintained dataset.\n\n" +
		"## Content\n\n" +
		"- Released albums and singles\n" +
		"- Unreleased music, demos, snippets and leaks\n" +
		"- News and release announcements\n" +
		"- Community notes on status and availability\n\n" +
		"## Key topics\n\n" +
		"- " + artist + " discography\n" +
		"- " + artist + " unreleased music\n" +
		"- " + artist + " music leaks\n" +
		"- " + artist + " news and releases\n" +
		"- tracking unreleased and leaked music\n"
	writeFile(filepath.Join(job.WwwDir, "llms-full.txt"), []byte(llmsFull))

	txt := `User-agent: *
Allow: /

# AI and answer-engine crawlers
User-agent: GPTBot
Allow: /

User-agent: ChatGPT-User
Allow: /

User-agent: Google-Extended
Allow: /

User-agent: ClaudeBot
Allow: /

User-agent: anthropic-ai
Allow: /

User-agent: PerplexityBot
Allow: /

User-agent: CCBot
Allow: /

User-agent: Bingbot
Allow: /
`
	writeFile(filepath.Join(job.WwwDir, "robots.txt"), []byte(txt))
}

func generate(ctx context.Context, job *Job) {
	job.mu.Lock()
	defer job.mu.Unlock()

	t0 := time.Now()
	fmt.Printf("[%s] [%s] generate start\n", time.Now().UTC().Format(time.RFC3339), job.SheetURL)

	job.cssImportSeen = sync.Map{}

	mkdirAll(job.WwwDir)
	mkdirAll(filepath.Join(job.WwwDir, "assets"))
	mkdirAll(filepath.Join(job.WwwDir, "assets", "css"))
	mkdirAll(filepath.Join(job.WwwDir, job.Mode, "sheet"))

	job.LastUpdate = time.Now().UTC()

	fetchFavicon(job)

	fmt.Println("  fetching main page...")
	mainHTML := fetchTransformedMain(job)
	if mainHTML == "" {
		return
	}

	gids := extractGids(mainHTML)
	fmt.Printf("  main page ok, %d tabs to fetch\n", len(gids))

	var mu sync.Mutex
	var tabs []tabResult

	tabFns := make([]func(), len(gids))
	for i, gid := range gids {
		gid := gid
		tabFns[i] = func() {
			html := fetchTransformedTab(gid, job)
			if html != "" {
				mu.Lock()
				tabs = append(tabs, tabResult{gid, html})
				mu.Unlock()
				fmt.Printf("    tab %s ok\n", gid)
			}
		}
	}
	pAll(tabFns, concurrency)

	if ctx.Err() != nil {
		return
	}

	sort.Slice(tabs, func(i, j int) bool { return tabs[i].gid < tabs[j].gid })

	h := sha256.New()
	h.Write([]byte(mainHTML))
	for _, t := range tabs {
		h.Write([]byte(t.gid))
		h.Write([]byte(t.html))
	}
	newHash := fmt.Sprintf("%x", h.Sum(nil))
	if job.LastHash == newHash {
		if _, err := os.Stat(filepath.Join(job.WwwDir, "index.html")); err == nil {
			fmt.Println("  no changes since last poll, skipping")
			return
		}
	}

	for _, t := range tabs {
		old1 := `\/` + job.Mode + `\/sheet?headers\x3dtrue&gid=` + t.gid
		old2 := `\/` + job.Mode + `\/sheet?gid=` + t.gid
		new := `\/` + job.Mode + `\/sheet\/` + t.gid + `.html`
		mainHTML = strings.ReplaceAll(mainHTML, old1, new)
		mainHTML = strings.ReplaceAll(mainHTML, old2, new)
	}

	escapedPrefix := `https:\/\/docs.google.com` + strings.ReplaceAll(job.SheetPath, "/", `\/`)
	mainHTML = strings.ReplaceAll(mainHTML, escapedPrefix, "")
	mainHTML = strings.ReplaceAll(mainHTML, "https://docs.google.com"+job.SheetPath, "")

	externalSet := make(map[string]bool)
	var externalList []string
	allHTMLs := append([]string{mainHTML}, func() []string {
		var h []string
		for _, t := range tabs {
			h = append(h, t.html)
		}
		return h
	}()...)
	for _, h := range allHTMLs {
		for _, u := range extractExternalImageUrls(h) {
			if !externalSet[u] {
				externalSet[u] = true
				externalList = append(externalList, u)
			}
		}
	}
	fmt.Printf("  %d external images to localize\n", len(externalList))

	imageMap := make(map[string]string)
	imgMu := sync.Mutex{}
	imgOk, imgFail := 0, 0

	imgFns := make([]func(), len(externalList))
	for i, u := range externalList {
		u := u
		imgFns[i] = func() {
			local := localizeImage(u, job)
			imgMu.Lock()
			if local != "" {
				imageMap[u] = local
				imgOk++
			} else {
				imgFail++
			}
			imgMu.Unlock()
		}
	}
	pAll(imgFns, concurrency)
	fmt.Printf("  images done: %d ok, %d failed\n", imgOk, imgFail)

	if ctx.Err() != nil {
		return
	}

	applyImageMap := func(html string) string {
		for u, local := range imageMap {
			html = strings.ReplaceAll(html, u, local)
		}
		return html
	}
	mainHTML = applyImageMap(mainHTML)
	for i := range tabs {
		tabs[i].html = applyImageMap(tabs[i].html)
	}

	fmt.Println("  localizing inline styles...")
	mainHTML = localizeInlineStyles(mainHTML, job)
	for i := range tabs {
		tabs[i].html = localizeInlineStyles(tabs[i].html, job)
	}

	mainHTML = ensureOgImageType(mainHTML)
	for i := range tabs {
		tabs[i].html = ensureOgImageType(tabs[i].html)
	}

	mainHTML = ensureOgCard(mainHTML, job.WwwDir)
	for i := range tabs {
		tabs[i].html = ensureOgCard(tabs[i].html, job.WwwDir)
	}

	mainHTML = ensureOgImageDims(mainHTML, job.WwwDir)
	for i := range tabs {
		tabs[i].html = ensureOgImageDims(tabs[i].html, job.WwwDir)
	}

	referencedAssets := collectReferencedAssets(job.WwwDir, mainHTML, tabs)

	ogImage := ""
	if m := reOgImage.FindStringSubmatch(mainHTML); len(m) > 1 {
		ogImage = m[1]
	}
	writePWAOutputs(job, ogImage)

	mainHTML = ensureAppleTouchIcon(mainHTML, job.WwwDir)
	for i := range tabs {
		tabs[i].html = ensureAppleTouchIcon(tabs[i].html, job.WwwDir)
	}

	writeFile(filepath.Join(job.WwwDir, "index.html"), []byte(mainHTML))
	for _, t := range tabs {
		writeFile(filepath.Join(job.WwwDir, job.Mode, "sheet", t.gid+".html"), []byte(t.html))
	}

	assetSet := make(map[string]bool)
	for _, p := range extractAssetPaths(mainHTML, job) {
		assetSet[p] = true
	}
	for _, t := range tabs {
		for _, p := range extractAssetPaths(t.html, job) {
			assetSet[p] = true
		}
	}
	assetList := make([]string, 0, len(assetSet))
	for p := range assetSet {
		assetList = append(assetList, p)
	}
	fmt.Printf("  %d static assets to download\n", len(assetList))

	if ctx.Err() != nil {
		return
	}

	assetFns := make([]func(), len(assetList))
	for i, p := range assetList {
		p := p
		assetFns[i] = func() { downloadAsset(p, job) }
	}
	pAll(assetFns, concurrency)

	cleanupStaleAssets(referencedAssets, job.WwwDir)

	if ctx.Err() == nil {
		gitPush(ctx, job)
	}

	job.LastHash = newHash

	elapsed := time.Since(t0).Seconds()
	fmt.Printf("  done in %.1fs - index.html (%dB), %d tabs, %d assets, %d images\n",
		elapsed, len(mainHTML), len(tabs), len(assetList), len(imageMap))
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	for _, job := range jobs {
		job := job
		wg.Add(1)
		go func() {
			defer wg.Done()
			generate(ctx, job)
			ticker := time.NewTicker(time.Duration(job.PollMinutes) * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					generate(ctx, job)
				}
			}
		}()
	}

	wg.Wait()
	fmt.Println("\nshutting down")
}
