package app

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Emby 反向代理：把 Emby 的播放请求拦截下来，直接 302 到 123 云盘 CDN 直链，
// 让客户端绕过 Emby 服务端代理、直接从 CDN 拉流（真正的客户端 302 播放）。

// embyStreamPathRe: 匹配 Emby/Jellyfin 的直连播放流地址
// 例如 /Videos/123/stream 、 /emby/Videos/123/stream 、 /mediabrowser/Videos/123/stream
var embyStreamPathRe = regexp.MustCompile(`^/(?:emby|mediabrowser)?/?Videos/([^/]+)/stream$`)

func (a *App) embyProxyEnabled() bool {
	return asBool(a.cfg.Config()["emby_proxy_enabled"])
}

func (a *App) embyProxyPort() int {
	p := int(firstInt64(a.cfg.Config(), "emby_proxy_port"))
	if p <= 0 {
		p = 8098
	}
	return p
}

func (a *App) embyURL() string {
	u := strings.TrimRight(asString(a.cfg.Config()["emby_url"]), "/")
	if u == "" {
		u = "http://127.0.0.1:8096"
	}
	return u
}

func (a *App) embyAPIKey() string {
	return asString(a.cfg.Config()["emby_api_key"])
}

// startEmbyProxy: 常驻监听反向代理端口；开关按请求动态读取，改配置无需重启
func (a *App) startEmbyProxy() {
	port := a.embyProxyPort()
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	log.Printf("[Emby反向代理] 监听 %s (目标: %s, 开关: %v)", addr, a.embyURL(), a.embyProxyEnabled())
	if err := http.ListenAndServe(addr, a.embyProxyHandler()); err != nil {
		log.Printf("[Emby反向代理] 监听失败: %v", err)
	}
}

func (a *App) embyProxyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.embyProxyEnabled() {
			http.Error(w, "Emby reverse proxy disabled", http.StatusNotFound)
			return
		}
		// 直连播放：302 到 CDN 直链
		if a.tryStreamRedirect(w, r) {
			return
		}
		// 其余请求反向代理到 Emby
		a.embyReverseProxy().ServeHTTP(w, r)
	})
}

func (a *App) embyReverseProxy() http.Handler {
	target, err := url.Parse(a.embyURL())
	if err != nil {
		target, _ = url.Parse("http://127.0.0.1:8096")
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("[Emby反向代理] 转发失败 %s: %v", r.URL.Path, err)
		http.Error(w, "Emby proxy error", http.StatusBadGateway)
	}
	return proxy
}

// tryStreamRedirect: 命中直连播放请求时返回 true 并已写出 302。
// 未命中 / 解析失败 / 取直链失败时返回 false，由调用方回退到普通反向代理。
func (a *App) tryStreamRedirect(w http.ResponseWriter, r *http.Request) bool {
	m := embyStreamPathRe.FindStringSubmatch(r.URL.Path)
	if m == nil {
		return false
	}
	itemID := m[1]
	playURL := a.embyItemPlayURL(r, itemID)
	if playURL == "" {
		return false
	}

	// 若为 /play 直链中转地址，解析出 etag/size/filename 后取最终 CDN 直链（单次 302）
	if etag, size, filename, ok := parsePlayURL(playURL); ok {
		fastMode := a.cfg.Config()["mode"] == "fast"
		direct := a.getFileURLWithEtagCandidates(filename, etag, size, fastMode)
		if direct != "" && !strings.Contains(direct, "222.186.21.40:33333/NGGYU.mp4") {
			http.Redirect(w, r, direct, http.StatusFound)
			return true
		}
		// 取直链失败：回退 302 到 /play，由 /play 自带的重试/占位逻辑兜底
		http.Redirect(w, r, playURL, http.StatusFound)
		return true
	}

	// 非 /play 的其它直链（如 STRM 内容本身就是 CDN 地址），直接 302
	http.Redirect(w, r, playURL, http.StatusFound)
	return true
}

// embyItemPlayURL: 查询 Emby 条目，返回其 STRM 内容里的媒体 URL（/play 或直链）
func (a *App) embyItemPlayURL(r *http.Request, itemID string) string {
	item := a.embyItem(r, itemID)
	if item == nil {
		return ""
	}
	// MediaSources[0].Path 通常就是 STRM 文件内容（即媒体 URL）
	if ms := asAnySlice(item["MediaSources"]); len(ms) > 0 {
		if m, ok := ms[0].(map[string]any); ok {
			if p := asString(m["Path"]); isHTTPURL(p) {
				return p
			}
		}
	}
	// 回退：条目 Path 可能是 .strm 文件路径，读取文件内容
	p := asString(item["Path"])
	if strings.HasSuffix(strings.ToLower(p), ".strm") {
		if u := readStrmURL(p); u != "" {
			return u
		}
	}
	if isHTTPURL(p) {
		return p
	}
	return ""
}

// embyItem: 查询 Emby 条目信息；优先用配置的 API Key，否则回退使用客户端 token
func (a *App) embyItem(r *http.Request, itemID string) map[string]any {
	base := strings.TrimRight(a.embyURL(), "/")
	u := base + "/emby/Items/" + url.PathEscape(itemID) + "?Fields=Path,MediaSources"
	apiKey := a.embyAPIKey()
	if apiKey == "" {
		if tok := r.Header.Get("X-Emby-Token"); tok != "" {
			apiKey = tok
		} else if tok := r.URL.Query().Get("api_key"); tok != "" {
			apiKey = tok
		}
	}
	if apiKey != "" {
		u += "&api_key=" + url.QueryEscape(apiKey)
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Emby反向代理] 查询 Emby 条目 %s 失败: %v", itemID, err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal(b, &data); err != nil {
		return nil
	}
	return data
}

// parsePlayURL: 解析 /play/{id}/{etag}/{size}/{filename}，提取 etag/size/filename
func parsePlayURL(playURL string) (etag string, size int64, filename string, ok bool) {
	idx := strings.Index(playURL, "/play/")
	if idx < 0 {
		return "", 0, "", false
	}
	rest := strings.TrimSpace(playURL[idx+len("/play/"):])
	parts := strings.Split(rest, "/")
	if len(parts) < 4 {
		return "", 0, "", false
	}
	etag = parts[1]
	if e, err := url.PathUnescape(etag); err == nil {
		etag = e
	}
	n, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return "", 0, "", false
	}
	filename = strings.Join(parts[3:], "/")
	if f, err := url.PathUnescape(filename); err == nil {
		filename = f
	}
	return etag, n, filename, true
}

func readStrmURL(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if isHTTPURL(line) {
			return line
		}
	}
	return ""
}

func isHTTPURL(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
