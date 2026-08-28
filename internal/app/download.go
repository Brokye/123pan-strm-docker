package app

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// downloadSettings: 附属文件下载配置（由 config.json 驱动）
type downloadSettings struct {
	enabled bool
	types   map[string]bool // subtitle / nfo / image
	threads int
	retries int
}

// wants: 指定类型是否启用下载
func (s downloadSettings) wants(t string) bool {
	return s.enabled && s.types[t]
}

// getDownloadSettings: 从配置读取并规范化下载设置
func (a *App) getDownloadSettings() downloadSettings {
	cfg := a.cfg.Config()
	enabled := asBool(cfg["download_enabled"])

	types := map[string]bool{}
	switch v := cfg["download_types"].(type) {
	case []any:
		for _, t := range v {
			if s, ok := t.(string); ok {
				types[s] = true
			}
		}
	case []string:
		for _, s := range v {
			types[s] = true
		}
	}
	// 兼容旧版：未显式配置下载类型时，若开启了 include_subtitles 则至少下载字幕
	if len(types) == 0 && asBool(cfg["include_subtitles"]) {
		types["subtitle"] = true
		enabled = true
	}

	threads := int(firstInt64(cfg, "download_threads"))
	if threads <= 0 {
		threads = 4
	}
	if threads > 32 {
		threads = 32
	}
	retries := int(firstInt64(cfg, "download_retries"))
	if retries < 0 {
		retries = 3
	}
	return downloadSettings{enabled: enabled, types: types, threads: threads, retries: retries}
}

func retryBackoff(attempt int) time.Duration {
	// 第 1 次重试等待 1s，随后 2s、3s... 线性递增
	return time.Duration(attempt+1) * time.Second
}

// httpDownload: 拉取直链并流式写入 targetPath（磁盘）
func (a *App) httpDownload(url, targetPath string) bool {
	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Referer", "https://yun.123pan.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return false
	}
	f, err := os.Create(targetPath)
	if err != nil {
		return false
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(targetPath)
		return false
	}
	return true
}

// downloadSidecarFile: 下载单个附属文件（字幕/nfo/图片），带重试逻辑。
// 每次重试都会重新获取直链，避免直链过期导致的失败。
func (a *App) downloadSidecarFile(fileInfo map[string]any, targetPath string, fastMode bool, retries int) bool {
	name := filepath.Base(asString(fileInfo["path"]))
	etag := asString(fileInfo["etag"])
	size := firstInt64(fileInfo, "size")

	for attempt := 0; attempt <= retries; attempt++ {
		url := a.getFileURLWithEtagCandidates(name, etag, size, fastMode)
		if url == "" || strings.Contains(url, "222.186.21.40:33333/NGGYU.mp4") {
			log.Printf("[下载] 获取直链失败(第%d/%d次): %s", attempt+1, retries+1, name)
			if attempt < retries {
				time.Sleep(retryBackoff(attempt))
				continue
			}
			return false
		}
		if a.httpDownload(url, targetPath) {
			log.Printf("[下载] 已下载: %s", targetPath)
			return true
		}
		log.Printf("[下载] 下载失败(第%d/%d次): %s", attempt+1, retries+1, name)
		if attempt < retries {
			time.Sleep(retryBackoff(attempt))
		}
	}
	return false
}

// downloadSubtitleFile: 兼容定时归档(archive.go)等旧调用方
func (a *App) downloadSubtitleFile(fileInfo map[string]any, targetPath string, fastMode bool) bool {
	return a.downloadSidecarFile(fileInfo, targetPath, fastMode, 3)
}
