package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	sheetURL    = ""
	sheetPath   = ""
	wwwDir      = "./www"
	pollMinutes = 10
	gitRepo     = ""
	gitPAT      = ""
	gitEmail    = ""
	gitName     = ""
	pageTitle   = "Frank Tracker"
	concurrency = 8
	maxBodySize = int64(100 * 1024 * 1024)
	imgThresholdBytes = 24 * 1024 * 1024
	jpegQuality = 85

	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124 Safari/537.36"

	reTelemetry       = regexp.MustCompile(`(?s)<script\b[^>]*>\s*window\['ppConfig'\].*?</script>`)
	reImgTag          = regexp.MustCompile(`(?i)<img\b`)
	rePointerNone     = regexp.MustCompile(`(?i)(<img\b[^>]*style="[^"]*?)pointer-events:\s*none`)
	reSizeSuffix      = regexp.MustCompile(`=w\d+(?:-h\d+)?`)
	reSupportRedirect = regexp.MustCompile(`window\.location\.href\s*=\s*'[^']*support\.google\.com[^']*'`)
	reGoogleRedirect  = regexp.MustCompile(`(href=")https://www\.google\.com/url\?q=([^&"]+)[^"]*(")`)
	reGid             = regexp.MustCompile(`gid=(\d+)`)
	reAssetPath       = regexp.MustCompile(`(?:src|href)=['"](/(?:static|_|htmlview)/[^'"]+)['"]`)
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
	reDocTitle        = regexp.MustCompile(`(?s)<span\s+class="name">[^<]*</span>`)
	reFirstLink       = regexp.MustCompile(`(?s)(<link\s[^>]*rel=['"]stylesheet['"][^>]*>)`)
	reReferencedAsset = regexp.MustCompile(`(?:src|href)=['"](/assets/[^'"]+)['"]`)

	client = &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	cssImportSeen sync.Map
	genMu         sync.Mutex
)

func init() {
	u := strings.TrimRight(os.Getenv("SHEET_URL"), "/")
	u = strings.TrimSuffix(u, "/htmlview")
	if u == "" {
		fmt.Fprintln(os.Stderr, "SHEET_URL required")
		os.Exit(1)
	}
	sheetURL = u
	u2, err := url.Parse(u)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid SHEET_URL:", err)
		os.Exit(1)
	}
	sheetPath = u2.Path

	if d := os.Getenv("WWW_DIR"); d != "" {
		wwwDir = strings.TrimRight(d, "/")
	}
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
	if v := os.Getenv("PAGE_TITLE"); v != "" {
		pageTitle = v
	}
	if v := os.Getenv("JPEG_QUALITY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			jpegQuality = n
		}
	}
	gitRepo = os.Getenv("GIT_REPO")
	gitPAT = os.Getenv("GIT_PAT")
	gitEmail = os.Getenv("GIT_EMAIL")
	gitName = os.Getenv("GIT_NAME")
	if gitEmail == "" {
		gitEmail = "sheetproxy@local"
	}
	if gitName == "" {
		gitName = "sheetproxy"
	}
}

func sha1hex(s string) string {
	h := sha1.Sum([]byte(s))
	return fmt.Sprintf("%x", h[:10])
}

func extFromContentType(ct string) string {
	if strings.Contains(ct, "svg") {
		return "svg"
	}
	if strings.Contains(ct, "png") {
		return "png"
	}
	if strings.Contains(ct, "jpeg") {
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
	return "bin"
}

func extFromFilename(path string) string {
	m := reExtFromPath.FindStringSubmatch(path)
	if len(m) > 1 {
		return m[1]
	}
	return "bin"
}

func commonTransform(html string) string {
	html = reTelemetry.ReplaceAllString(html, "")
	html = reImgTag.ReplaceAllString(html, `<img crossorigin="anonymous" referrerpolicy="no-referrer"`)
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
	html = reTitle.ReplaceAllString(html, `<title>`+pageTitle+`</title>`)
	html = reOgTitle.ReplaceAllString(html, `<meta property="og:title" content="`+pageTitle+`">`)
	html = reDocTitle.ReplaceAllString(html, `<span class="name">`+pageTitle+`</span>`)

	coollabsLink := `<link href="https://api.fonts.coollabs.io/css2?family=Helvetica+Neue&display=swap" rel="stylesheet">`
	if !strings.Contains(html, "coollabs.io") {
		if m := reFirstLink.FindStringIndex(html); m != nil {
			html = html[:m[0]] + coollabsLink + html[m[0]:]
		} else if idx := strings.Index(html, "</title>"); idx != -1 {
			html = html[:idx+len("</title>")] + coollabsLink + html[idx+len("</title>"):]
		} else if idx := strings.Index(html, "</head>"); idx != -1 {
			html = html[:idx] + coollabsLink + html[idx:]
		}
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

func extractAssetPaths(html string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, m := range reAssetPath.FindAllStringSubmatch(html, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			result = append(result, m[1])
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

func mkdirAll(path string) {
	if err := os.MkdirAll(path, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "  mkdir %s: %v\n", path, err)
	}
}

func writeFile(path string, data []byte) {
	if err := os.WriteFile(path, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "  write %s: %v\n", path, err)
	}
}

func localizeImage(rawURL string) string {
	name := sha1hex(rawURL)
	for _, ext := range []string{"png", "jpg", "gif", "webp", "ico", "bin"} {
		dest := filepath.Join(wwwDir, "assets", name+"."+ext)
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

	if len(data) > imgThresholdBytes {
		cfg, _, decErr := image.DecodeConfig(bytes.NewReader(data))
		if decErr == nil && cfg.Width*cfg.Height > 16384*16384 {
			fmt.Fprintf(os.Stderr, "  skipping too-large image %s (%dx%d)\n", rawURL, cfg.Width, cfg.Height)
			return ""
		}
		if img, _, decErr := image.Decode(bytes.NewReader(data)); decErr == nil {
			bounds := img.Bounds()
			rgba := image.NewRGBA(bounds)
			draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)
			var pngBuf bytes.Buffer
			if png.Encode(&pngBuf, rgba) == nil && pngBuf.Len() < imgThresholdBytes {
				dest := filepath.Join(wwwDir, "assets", name+".png")
				writeFile(dest, pngBuf.Bytes())
				return "/assets/" + name + ".png"
			}
			var jpgBuf bytes.Buffer
			if jpeg.Encode(&jpgBuf, rgba, &jpeg.Options{Quality: jpegQuality}) == nil {
				dest := filepath.Join(wwwDir, "assets", name+".jpg")
				writeFile(dest, jpgBuf.Bytes())
				return "/assets/" + name + ".jpg"
			}
		}
	}

	ct := resp.Header.Get("Content-Type")
	ext := extFromContentType(ct)
	dest := filepath.Join(wwwDir, "assets", name+"."+ext)

	writeFile(dest, data)
	return "/assets/" + name + "." + ext
}

func fetchTransformedMain() string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := httpGetRetry(ctx, sheetURL+"/htmlview", 2)
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

	plain := "https://docs.google.com" + sheetPath
	escaped := strings.ReplaceAll("https:\\/\\/docs.google.com"+strings.ReplaceAll(sheetPath, "/", "\\/"), "/", "\\/")
	html = strings.ReplaceAll(html, plain, "")
	html = strings.ReplaceAll(html, escaped, "")

	return commonTransform(html)
}

func fetchTransformedTab(gid string) string {
	target := fmt.Sprintf("https://docs.google.com%s/htmlview/sheet?headers=true&gid=%s", sheetPath, gid)
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
	return commonTransform(string(data))
}

func localizeCssAsset(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	ext := extFromFilename(u.Path)
	name := sha1hex(rawURL)
	dest := filepath.Join(wwwDir, "assets", "css", name+"."+ext)
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
	writeFile(dest, data)
	fmt.Println("  css asset", rawURL)
	return "/assets/css/" + name + "." + ext
}

func localizeCss(css string, depth int) string {
	if depth < 3 {
		for _, m := range reCssImport.FindAllStringSubmatch(css, -1) {
			importURL := m[1]
			if strings.HasPrefix(importURL, "//") {
				importURL = "https:" + importURL
			}
			if reKitParam.MatchString(importURL) {
				continue
			}
			if _, loaded := cssImportSeen.LoadOrStore(importURL, true); loaded {
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
			localized := localizeCss(string(body), depth+1)
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
		local := localizeCssAsset(fetchURL)
		if local != "" {
			css = strings.ReplaceAll(css, u, local)
		}
	}
	return css
}

func localizeInlineStyles(html string) string {
	for _, m := range reInlineStyle.FindAllStringSubmatch(html, -1) {
		if !reHasExternal.MatchString(m[1]) {
			continue
		}
		localized := localizeCss(m[1], 0)
		if localized != m[1] {
			html = strings.Replace(html, m[0], "<style>"+localized+"</style>", 1)
		}
	}
	return html
}

func downloadAsset(path string) {
	dest := filepath.Join(wwwDir, path)
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
		css := localizeCss(string(data), 0)
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

func gitPush(ctx context.Context) {
	if gitRepo == "" || gitPAT == "" {
		fmt.Println("  git push skipped (GIT_REPO/GIT_PAT not set)")
		return
	}

	remoteURL := fmt.Sprintf("https://%s@github.com/%s.git", gitPAT, gitRepo)

	gitDir := filepath.Join(wwwDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		runGitCtx(ctx, "init", "-b", "main")
		runGitCtx(ctx, "config", "user.email", gitEmail)
		runGitCtx(ctx, "config", "user.name", gitName)
		runGitCtx(ctx, "remote", "add", "origin", remoteURL)
	} else {
		runGitCtx(ctx, "remote", "set-url", "origin", remoteURL)
	}

	runGitCtx(ctx, "add", "-A")
	if out, err := runGitCtx(ctx, "diff", "--cached", "--quiet"); err == nil && out == "" {
		fmt.Println("  no changes to commit")
		return
	}
	commitMsg := fmt.Sprintf("update %s", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))
	if out, err := runGitCtx(ctx, "commit", "-m", commitMsg); err != nil {
		fmt.Printf("  git commit: %s\n", out)
	}
	if out, err := runGitCtx(ctx, "push", "origin", "main"); err != nil {
		fmt.Printf("  git push: %s\n", out)
	} else {
		fmt.Println("  git push ok")
	}
}

func runGitCtx(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = wwwDir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func cleanupStaleAssets(referenced map[string]bool) {
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

func generate(ctx context.Context) {
	genMu.Lock()
	defer genMu.Unlock()

	t0 := time.Now()
	fmt.Printf("[%s] generate start\n", time.Now().UTC().Format(time.RFC3339))

	cssImportSeen = sync.Map{}

	mkdirAll(wwwDir)
	mkdirAll(filepath.Join(wwwDir, "assets"))
	mkdirAll(filepath.Join(wwwDir, "assets", "css"))
	mkdirAll(filepath.Join(wwwDir, "htmlview", "sheet"))

	fmt.Println("  fetching main page...")
	mainHTML := fetchTransformedMain()
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
			html := fetchTransformedTab(gid)
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

	for _, t := range tabs {
		old := `\/htmlview\/sheet?headers\x3dtrue&gid=` + t.gid
		new := `\/htmlview\/sheet\/` + t.gid + `.html`
		mainHTML = strings.ReplaceAll(mainHTML, old, new)
	}

	escapedPrefix := `https:\/\/docs.google.com` + strings.ReplaceAll(sheetPath, "/", `\/`)
	mainHTML = strings.ReplaceAll(mainHTML, escapedPrefix, "")
	mainHTML = strings.ReplaceAll(mainHTML, "https://docs.google.com"+sheetPath, "")

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
			local := localizeImage(u)
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
	mainHTML = localizeInlineStyles(mainHTML)
	for i := range tabs {
		tabs[i].html = localizeInlineStyles(tabs[i].html)
	}

	reReferencedAsset := regexp.MustCompile(`(?:src|href)=['"](/assets/[^'"]+)['"]`)
	referencedAssets := make(map[string]bool)
	for _, h := range append([]string{mainHTML}, func() []string {
		var h []string
		for _, t := range tabs {
			h = append(h, t.html)
		}
		return h
	}()...) {
		for _, m := range reReferencedAsset.FindAllStringSubmatch(h, -1) {
			referencedAssets[m[1]] = true
		}
	}

	writeFile(filepath.Join(wwwDir, "index.html"), []byte(mainHTML))
	for _, t := range tabs {
		writeFile(filepath.Join(wwwDir, "htmlview", "sheet", t.gid+".html"), []byte(t.html))
	}

	assetSet := make(map[string]bool)
	for _, p := range extractAssetPaths(mainHTML) {
		assetSet[p] = true
	}
	for _, t := range tabs {
		for _, p := range extractAssetPaths(t.html) {
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
		assetFns[i] = func() { downloadAsset(p) }
	}
	pAll(assetFns, concurrency)

	if ctx.Err() == nil {
		gitPush(ctx)
	}

	cleanupStaleAssets(referencedAssets)

	elapsed := time.Since(t0).Seconds()
	fmt.Printf("  done in %.1fs - index.html (%dB), %d tabs, %d assets, %d images\n",
		elapsed, len(mainHTML), len(tabs), len(assetList), len(imageMap))
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	generate(ctx)

	ticker := time.NewTicker(time.Duration(pollMinutes) * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nshutting down")
			return
		case <-ticker.C:
			generate(ctx)
		}
	}
}
