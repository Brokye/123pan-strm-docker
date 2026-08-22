package app

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func makePlayURL(base string, fileID int, etag string, size int64, filename string) string {
	etagEnc := url.PathEscape(etag)
	fnEnc := url.PathEscape(filename)
	return strings.TrimRight(base, "/") + fmt.Sprintf("/play/%d/%s/%d/%s", fileID, etagEnc, size, fnEnc)
}

func (a *App) downloadSubtitleFile(fileInfo map[string]any, targetPath string, fastMode bool) bool {
	name := filepath.Base(asString(fileInfo["path"]))
	url := a.getFileURLWithEtagCandidates(name, asString(fileInfo["etag"]), firstInt64(fileInfo, "size"), fastMode)
	if url == "" || strings.Contains(url, "222.186.21.40:33333/NGGYU.mp4") {
		log.Printf("[字幕] 获取直链失败，跳过: %s", name)
		return false
	}
	client := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Referer", "https://yun.123pan.com/")
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[字幕] 下载失败: %s: %v", name, err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return false
		}
		os.MkdirAll(filepath.Dir(targetPath), 0o755)
		os.WriteFile(targetPath, b, 0o644)
		log.Printf("[字幕] 已下载: %s", targetPath)
		return true
	}
	log.Printf("[字幕] 下载状态异常 %d: %s", resp.StatusCode, name)
	return false
}

// generateStrmTask: 先生成 STRM，再下载字幕（带百分比进度）
// progress 回调返回事件 map
func (a *App) generateStrmTask(libID, outputDir, serverBase string, includeSubtitles bool) func(emit func(map[string]any)) {
	return func(emit func(map[string]any)) {
		lib, err := a.cfg.LoadLib(libID)
		if err != nil {
			panic("library not found")
		}
		category, _ := lib["category"].(string)
		cfg := a.cfg.Config()
		fastMode := cfg["mode"] == "fast"
		outRoot := filepath.Clean(outputDir)
		if sub, ok := CATEGORY_DIRS[category]; ok {
			outRoot = filepath.Join(outRoot, sub)
		}

		type fl struct {
			path string
			idx  int
			etag string
			size int64
		}
		var videos, subs []fl
		files, _ := lib["files"].([]any)
		for _, f := range files {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			rel := safeRelPath(asString(fm["path"]))
			ext := strings.ToLower(filepath.Ext(rel))
			item := fl{path: asString(fm["path"]), idx: int(firstInt64(fm, "idx")), etag: asString(fm["etag"]), size: firstInt64(fm, "size")}
			if VIDEO_EXTS[ext] {
				videos = append(videos, item)
			} else if includeSubtitles && SUBTITLE_EXTS[ext] {
				subs = append(subs, item)
			}
		}

		count := 0
		subtitles := 0
		skipped := 0
		examples := []string{}

		// 第一阶段：生成所有 STRM
		for i, f := range videos {
			rel := safeRelPath(f.path)
			target := filepath.Join(outRoot, relWithoutSuffix(rel, filepath.Ext(rel))+".strm")
			os.MkdirAll(filepath.Dir(target), 0o755)
			fileName := filepath.Base(rel)
			url := makePlayURL(serverBase, f.idx, f.etag, f.size, fileName)
			os.WriteFile(target, []byte(url+"\n"), 0o644)
			count++
			if len(examples) < 10 {
				examples = append(examples, target)
			}
			emit(map[string]any{
				"message":  fmt.Sprintf("正在生成 %s (%d/%d)...", rel, i+1, len(videos)),
				"progress": int((i + 1) * 80 / maxInt(len(videos), 1)),
			})
		}

		// 第二阶段：下载字幕
		if len(subs) > 0 {
			emit(map[string]any{"message": "生成完成，正在下载字幕...", "progress": 80})
			for j, f := range subs {
				rel := safeRelPath(f.path)
				target := filepath.Join(outRoot, rel)
				if a.downloadSubtitleFile(map[string]any{"path": f.path, "etag": f.etag, "size": f.size}, target, fastMode) {
					subtitles++
				} else {
					skipped++
				}
				if len(examples) < 10 {
					examples = append(examples, target)
				}
				emit(map[string]any{
					"message":  fmt.Sprintf("正在下载字幕 (%d/%d)...", j+1, len(subs)),
					"progress": 80 + int((j+1)*20/len(subs)),
				})
			}
		} else {
			emit(map[string]any{"message": "无字幕文件", "progress": 100})
		}

		emit(map[string]any{"result": map[string]any{
			"count":      count,
			"subtitles":  subtitles,
			"skipped":    skipped,
			"output_dir": outRoot,
			"examples":   examples,
		}})
		log.Printf("[生成] 库 %s 完成: 生成 STRM %d 个, 字幕 %d 个, 跳过 %d 个 → %s",
			libID, count, subtitles, skipped, outRoot)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func relWithoutSuffix(rel, ext string) string {
	return strings.TrimSuffix(rel, ext)
}

// syncAllStrmTask: 同步所有库
func (a *App) syncAllStrmTask(outputDir, serverBase string, includeSubtitles bool) func(emit func(map[string]any)) {
	return func(emit func(map[string]any)) {
		emit(map[string]any{"message": "准备同步所有库...", "progress": 0})
		result := a.syncAllLibraries(outputDir, serverBase, includeSubtitles)
		emit(map[string]any{
			"message":  fmt.Sprintf("同步完成: 删除 %d 个, 生成 %d 个", result["total_to_delete_strm"], result["total_to_create_strm"]),
			"progress": 100,
			"result":   result,
		})
	}
}

// panExportTask: 多线程递归扫描账号盘选中内容，生成秒传 JSON（带进度）
func (a *App) panExportTask(driver *Pan123, folders, files []map[string]any) func(emit func(map[string]any)) {
	return func(emit func(map[string]any)) {
		emit(map[string]any{"message": "准备扫描网盘目录...", "progress": 0})
		folderPaths := []string{}
		for _, f := range folders {
			folderPaths = append(folderPaths, strings.Trim(asString(f["path"]), "/"))
		}
		underFolder := func(p string) bool {
			p = strings.Trim(p, "/")
			for _, fp := range folderPaths {
				if fp != "" && (p == fp || strings.HasPrefix(p, fp+"/")) {
					return true
				}
			}
			return false
		}
		plainFiles := []map[string]any{}
		for _, f := range files {
			if !underFolder(asString(f["path"])) {
				plainFiles = append(plainFiles, f)
			}
		}

		type task struct {
			fid  int64
			path string
			name string
		}
		queue := []task{}
		for i, fd := range folders {
			queue = append(queue, task{fid: int64(asFloat(fd["fileId"])), path: folderPaths[i], name: asString(fd["name"])})
		}
		discovered := len(queue)
		processed := 0
		type scanned struct {
			path string
			size int64
			etag string
			name string
		}
		scannedFiles := map[int64]scanned{}
		errors := []string{}
		var mu sync.Mutex

		// 用有限 goroutine 池逐层扫描，全局并发上限 16，避免触发 123 网盘 API 限流
		sem := make(chan struct{}, 16)
		for len(queue) > 0 {
			batch := queue
			queue = []task{}
			var wg sync.WaitGroup
			type scanResult struct {
				t   task
				out []map[string]any
				err string
			}
			results := make(chan scanResult, len(batch))
			for _, t := range batch {
				wg.Add(1)
				go func(t task) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					res := driver.listFilesSingle(t.fid)
					if e, ok := res["error"]; ok {
						results <- scanResult{t: t, err: asString(e)}
						return
					}
					out := []map[string]any{}
					items, _ := res["items"].([]any)
					for _, it := range items {
						im, _ := it.(map[string]any)
						cname := asString(im["FileName"])
						cpath := cname
						if t.path != "" {
							cpath = t.path + "/" + cname
						}
						if typ, _ := im["Type"].(float64); typ == 1 {
							out = append(out, map[string]any{"kind": "folder", "fid": asFloat(im["FileId"]), "path": cpath, "name": cname})
						} else {
							out = append(out, map[string]any{"kind": "file", "item": im, "path": cpath})
						}
					}
					results <- scanResult{t: t, out: out}
				}(t)
			}
			wg.Wait()
			close(results)
			for r := range results {
				mu.Lock()
				processed++
				if r.err != "" {
					errors = append(errors, r.t.name+": "+r.err)
				} else {
					for _, o := range r.out {
						if o["kind"] == "folder" {
							discovered++
							queue = append(queue, task{fid: int64(o["fid"].(float64)), path: asString(o["path"]), name: asString(o["name"])})
						} else {
							im, _ := o["item"].(map[string]any)
							fid := int64(asFloat(im["FileId"]))
							if _, ok := scannedFiles[fid]; !ok {
								scannedFiles[fid] = scanned{
									path: asString(o["path"]),
									size: int64(asFloat(im["Size"])),
									etag: asString(im["Etag"]),
									name: asString(im["FileName"]),
								}
							}
						}
					}
				}
				pct := 99
				if discovered > 0 {
					pct = processed * 100 / discovered
					if pct > 99 {
						pct = 99
					}
				}
				mu.Unlock()
				emit(map[string]any{"message": fmt.Sprintf("正在扫描文件夹 %d/%d...", processed, discovered), "progress": pct})
			}
		}

		for _, f := range plainFiles {
			fid := int64(asFloat(f["fileId"]))
			if fid != 0 {
				if _, ok := scannedFiles[fid]; !ok {
					p := asString(f["path"])
					if p == "" {
						p = asString(f["name"])
					}
					scannedFiles[fid] = scanned{
						path: strings.Trim(p, "/"),
						size: int64(asFloat(f["size"])),
						etag: asString(f["etag"]),
						name: asString(f["name"]),
					}
				}
			}
		}

		emit(map[string]any{"message": "扫描完成，正在生成秒传 JSON...", "progress": 99})
		filesOut := []map[string]any{}
		failed := 0
		for _, meta := range scannedFiles {
			if meta.path == "" || meta.etag == "" {
				failed++
				continue
			}
			filesOut = append(filesOut, map[string]any{
				"path": meta.path,
				"size": meta.size,
				"etag": toSecEtag(meta.etag),
			})
		}
		rootName := ""
		if len(folderPaths) > 0 {
			rootName = strings.Split(folderPaths[0], "/")[0]
		}
		common := ""
		if rootName != "" && len(folderPaths) == 1 {
			common = rootName + "/"
		}
		secJSON := map[string]any{
			"scriptVersion":           "114514",
			"exportVersion":           "114514",
			"usesBase62EtagsInExport": true,
			"commonPath":              common,
			"files":                   filesOut,
		}
		if len(errors) > 0 {
			secJSON["warnings"] = errors
		}
		failCount := failed + len(errors)
		emit(map[string]any{
			"message":  fmt.Sprintf("完成：成功 %d 个，失败 %d 个", len(filesOut), failCount),
			"progress": 100,
			"result":   secJSON,
		})
	}
}
