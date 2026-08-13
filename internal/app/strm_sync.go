package app

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func (a *App) catRoot(outputDir, category string) string {
	root := filepath.Clean(outputDir)
	if sub, ok := CATEGORY_DIRS[category]; ok {
		root = filepath.Join(root, sub)
	}
	return root
}

// getExpectedFiles: 计算期望生成的 STRM 和字幕文件
func (a *App) getExpectedFiles(lib map[string]any, outputDir string, includeSubtitles bool) ([]string, []string) {
	category, _ := lib["category"].(string)
	outRoot := a.catRoot(outputDir, category)
	var strmFiles, subtitleFiles []string
	files, _ := lib["files"].([]any)
	for _, f := range files {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		rel := safeRelPath(asString(fm["path"]))
		ext := strings.ToLower(filepath.Ext(rel))
		if VIDEO_EXTS[ext] {
			strmFiles = append(strmFiles, filepath.Join(outRoot, relWithoutSuffix(rel, ext)+".strm"))
		} else if includeSubtitles && SUBTITLE_EXTS[ext] {
			subtitleFiles = append(subtitleFiles, filepath.Join(outRoot, rel))
		}
	}
	return strmFiles, subtitleFiles
}

func getExistingFiles(scanRoot string) ([]string, []string) {
	if _, err := os.Stat(scanRoot); err != nil {
		return nil, nil
	}
	var strmFiles, subtitleFiles []string
	filepath.Walk(scanRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(p), ".strm") {
			strmFiles = append(strmFiles, p)
		} else if SUBTITLE_EXTS[strings.ToLower(filepath.Ext(p))] {
			subtitleFiles = append(subtitleFiles, p)
		}
		return nil
	})
	return strmFiles, subtitleFiles
}

func collectExpected(libs []map[string]any, outputDir string, includeSubtitles bool) (map[string]bool, map[string]bool, map[string]map[string]any, map[string]map[string]any) {
	strmMap := map[string]map[string]any{}
	subMap := map[string]map[string]any{}
	for _, lib := range libs {
		category, _ := lib["category"].(string)
		catRoot := a_catRoot(outputDir, category)
		files, _ := lib["files"].([]any)
		for _, f := range files {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			rel := safeRelPath(asString(fm["path"]))
			ext := strings.ToLower(filepath.Ext(rel))
			if VIDEO_EXTS[ext] {
				key := filepath.Join(catRoot, relWithoutSuffix(rel, ext)+".strm")
				if _, exists := strmMap[key]; !exists {
					strmMap[key] = fm
				}
			} else if includeSubtitles && SUBTITLE_EXTS[ext] {
				key := filepath.Join(catRoot, rel)
				if _, exists := subMap[key]; !exists {
					subMap[key] = fm
				}
			}
		}
	}
	strmSet := map[string]bool{}
	for k := range strmMap {
		strmSet[k] = true
	}
	subSet := map[string]bool{}
	for k := range subMap {
		subSet[k] = true
	}
	return strmSet, subSet, strmMap, subMap
}

func a_catRoot(outputDir, category string) string {
	root := filepath.Clean(outputDir)
	if sub, ok := CATEGORY_DIRS[category]; ok {
		root = filepath.Join(root, sub)
	}
	return root
}

// syncCore: 核心同步：对比期望与现有，删除多余、生成缺失
func (a *App) syncCore(expectedStrm, expectedSubs map[string]bool, strmMap, subMap map[string]map[string]any, outputDir, serverBase string, includeSubtitles bool, dryRun bool, label string, scanRoot string) map[string]any {
	outRootDir := filepath.Clean(outputDir)
	if scanRoot == "" {
		scanRoot = outRootDir
	}
	existingStrm, existingSubs := getExistingFiles(scanRoot)
	existingStrmSet := map[string]bool{}
	for _, f := range existingStrm {
		existingStrmSet[f] = true
	}
	existingSubsSet := map[string]bool{}
	for _, f := range existingSubs {
		existingSubsSet[f] = true
	}

	toDeleteStrm := setDiff(existingStrmSet, expectedStrm)
	toDeleteSubs := setDiff(existingSubsSet, expectedSubs)
	toCreateStrm := setDiff(expectedStrm, existingStrmSet)
	toCreateSubs := setDiff(expectedSubs, existingSubsSet)

	result := map[string]any{
		"lib_id":         label,
		"expected_strm":  len(expectedStrm),
		"expected_subs":  len(expectedSubs),
		"existing_strm":  len(existingStrm),
		"existing_subs":  len(existingSubs),
		"to_delete_strm": len(toDeleteStrm),
		"to_delete_subs": len(toDeleteSubs),
		"to_create_strm": len(toCreateStrm),
		"to_create_subs": len(toCreateSubs),
		"deleted_strm":   []string{},
		"deleted_subs":   []string{},
		"created_strm":   []string{},
		"created_subs":   []string{},
		"errors":         []string{},
	}

	if dryRun {
		result["dry_run"] = true
		return result
	}

	relOf := func(p string) string {
		r, err := filepath.Rel(outRootDir, p)
		if err != nil {
			return p
		}
		return r
	}

	// 删除多余的 STRM
	for f := range toDeleteStrm {
		if err := os.Remove(f); err != nil {
			result["errors"] = append(result["errors"].([]string), "删除 STRM 失败 "+f+": "+err.Error())
		} else {
			result["deleted_strm"] = append(result["deleted_strm"].([]string), relOf(f))
			log.Printf("[同步] 删除 STRM: %s", relOf(f))
		}
	}

	// 删除多余的字幕
	for f := range toDeleteSubs {
		if err := os.Remove(f); err != nil {
			result["errors"] = append(result["errors"].([]string), "删除字幕失败 "+f+": "+err.Error())
		} else {
			result["deleted_subs"] = append(result["deleted_subs"].([]string), relOf(f))
			log.Printf("[同步] 删除字幕: %s", relOf(f))
		}
	}

	// 清理空目录
	a.cleanupEmptyDirs(scanRoot, outRootDir)

	// 生成缺失的 STRM（并行 8）
	createStrm := func(strmPath string) {
		fileInfo, ok := strmMap[strmPath]
		if !ok {
			return
		}
		fileName := filepath.Base(safeRelPath(asString(fileInfo["path"])))
		url := makePlayURL(serverBase, int(firstInt64(fileInfo, "idx")), asString(fileInfo["etag"]), firstInt64(fileInfo, "size"), fileName)
		os.MkdirAll(filepath.Dir(strmPath), 0o755)
		os.WriteFile(strmPath, []byte(url+"\n"), 0o644)
		result["created_strm"] = append(result["created_strm"].([]string), relOf(strmPath))
	}
	parallelStrings(toCreateStrm, 8, func(s string) {
		if err := safeRun(func() { createStrm(s) }); err != nil {
			result["errors"] = append(result["errors"].([]string), "生成 STRM 失败 "+s+": "+err.Error())
		}
	})

	// 下载缺失的字幕
	if includeSubtitles {
		cfg := a.cfg.Config()
		loggedIn := a.panLoggedIn()
		if !loggedIn && len(toCreateSubs) > 0 {
			result["subtitle_skipped"] = "未登录 123 网盘，跳过字幕下载"
			return result
		}
		fastMode := cfg["mode"] == "fast"
		downloadSub := func(subPath string) {
			fileInfo, ok := subMap[subPath]
			if !ok {
				return
			}
			if a.downloadSubtitleFile(fileInfo, subPath, asBool(fastMode)) {
				result["created_subs"] = append(result["created_subs"].([]string), relOf(subPath))
			}
		}
		parallelStrings(toCreateSubs, 8, func(s string) {
			if err := safeRun(func() { downloadSub(s) }); err != nil {
				result["errors"] = append(result["errors"].([]string), "下载字幕失败 "+s+": "+err.Error())
			}
		})
	}
	return result
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1" || t == "fast"
	}
	return false
}

func (a *App) panLoggedIn() bool {
	cacheData := ReadJSONFile(a.cfg.CachePath)
	if cacheData == nil {
		return false
	}
	tok, _ := cacheData["accessToken"].(string)
	if tok == "" {
		return false
	}
	ct, ok := cacheData["tokenCreateTime"]
	if !ok {
		return false
	}
	var t int64
	switch v := ct.(type) {
	case float64:
		t = int64(v)
	case int64:
		t = v
	case string:
		t = parseInt(v)
	}
	return time.Now().Unix()-t < 25*24*60*60
}

func (a *App) cleanupEmptyDirs(baseDir, stopAt string) {
	if filepath.Clean(baseDir) == filepath.Clean(stopAt) {
		return
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			a.cleanupEmptyDirs(filepath.Join(baseDir, e.Name()), stopAt)
		}
	}
	entries2, _ := os.ReadDir(baseDir)
	if len(entries2) == 0 && baseDir != stopAt {
		os.Remove(baseDir)
	}
}

func setDiff(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		if !b[k] {
			out[k] = true
		}
	}
	return out
}

func safeRun(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmtErr(r)
		}
	}()
	f()
	return nil
}

func fmtErr(r any) error {
	if e, ok := r.(error); ok {
		return e
	}
	return errUnsupportedFormat
}

func parallelStrings(set map[string]bool, workers int, fn func(string)) {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	ch := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range ch {
				fn(s)
			}
		}()
	}
	for _, k := range keys {
		ch <- k
	}
	close(ch)
	wg.Wait()
}

// syncAllLibraries: 合并全部库的期望后整体对比
func (a *App) syncAllLibraries(outputDir, serverBase string, includeSubtitles bool) map[string]any {
	cfg := a.cfg.Config()
	if outputDir == "" {
		outputDir = asString(cfg["output_dir"])
	}
	if outputDir == "" {
		outputDir = a.cfg.DefaultOutDir
	}
	if serverBase == "" {
		serverBase = asString(cfg["server_base"])
	}
	if serverBase == "" {
		serverBase = a.cfg.defaultServerBase()
	}
	outRootDir := filepath.Clean(outputDir)

	var allLibs []map[string]any
	for _, info := range a.cfg.ListLibraries() {
		id, _ := info["id"].(string)
		if id == "" {
			continue
		}
		lib, err := a.cfg.LoadLib(id)
		if err == nil {
			allLibs = append(allLibs, lib)
		}
	}

	expectedStrm, expectedSubs, strmMap, subMap := collectExpected(allLibs, outputDir, includeSubtitles)
	result := a.syncCore(expectedStrm, expectedSubs, strmMap, subMap, outputDir, serverBase, includeSubtitles, false, "all", outRootDir)

	log.Printf("[同步] 完成: 删除 STRM %d 个 / 字幕 %d 个, 生成 STRM %d 个 / 字幕 %d 个, 错误 %d 个",
		len(result["deleted_strm"].([]string)),
		len(result["deleted_subs"].([]string)),
		len(result["created_strm"].([]string)),
		len(result["created_subs"].([]string)),
		len(result["errors"].([]string)))
	if errs := result["errors"].([]string); len(errs) > 0 {
		for _, e := range errs {
			log.Printf("[同步] 错误: %s", e)
		}
	}

	return map[string]any{
		"libraries":            result,
		"total_expected_strm":  result["expected_strm"],
		"total_existing_strm":  result["existing_strm"],
		"total_to_delete_strm": result["to_delete_strm"],
		"total_to_create_strm": result["to_create_strm"],
		"total_deleted_strm":   len(result["deleted_strm"].([]string)),
		"total_created_strm":   len(result["created_strm"].([]string)),
		"total_deleted_subs":   len(result["deleted_subs"].([]string)),
		"total_created_subs":   len(result["created_subs"].([]string)),
		"total_errors":         len(result["errors"].([]string)),
	}
}

// ==================== MD5(etag) 去重 ====================

// dedupLibrary: 单库内按 etag 完全一致去重，返回待删清单（不执行删除）
func (a *App) dedupLibrary(lib map[string]any) map[string]any {
	libID, _ := lib["id"].(string)
	libName, _ := lib["name"].(string)
	files, _ := lib["files"].([]any)

	videoByEtag := map[string][]map[string]any{}
	subByEtag := map[string][]map[string]any{}
	for _, f := range files {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		etag := strings.TrimSpace(asString(fm["etag"]))
		fpath := asString(fm["path"])
		if etag == "" || fpath == "" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(fpath))
		if VIDEO_EXTS[ext] {
			videoByEtag[etag] = append(videoByEtag[etag], fm)
		} else if SUBTITLE_EXTS[ext] {
			subByEtag[etag] = append(subByEtag[etag], fm)
		}
	}

	buildGroups := func(groups map[string][]map[string]any) []map[string]any {
		out := []map[string]any{}
		for etag, items := range groups {
			if len(items) < 2 {
				continue
			}
			filesOut := []map[string]any{}
			for _, f := range items {
				filesOut = append(filesOut, map[string]any{
					"idx":  firstInt64(f, "idx"),
					"path": asString(f["path"]),
					"size": firstInt64(f, "size"),
				})
			}
			out = append(out, map[string]any{
				"etag":  etag,
				"size":  firstInt64(items[0], "size"),
				"count": len(items),
				"files": filesOut,
			})
		}
		return out
	}

	videoGroups := buildGroups(videoByEtag)
	subGroups := buildGroups(subByEtag)
	videoCount := 0
	for _, g := range videoGroups {
		videoCount += int(firstInt64(g, "count"))
	}
	subCount := 0
	for _, g := range subGroups {
		subCount += int(firstInt64(g, "count"))
	}

	return map[string]any{
		"lib_id":          libID,
		"lib_name":        libName,
		"video_groups":    videoGroups,
		"sub_groups":      subGroups,
		"video_dup_count": videoCount,
		"sub_dup_count":   subCount,
	}
}

// dedupScanAll: 扫描全部库，返回去重清单（不执行删除）
func (a *App) dedupScanAll() map[string]any {
	var results []map[string]any
	totalVideo := 0
	totalSub := 0
	for _, info := range a.cfg.ListLibraries() {
		id, _ := info["id"].(string)
		if id == "" {
			continue
		}
		lib, err := a.cfg.LoadLib(id)
		if err != nil {
			continue
		}
		r := a.dedupLibrary(lib)
		if len(r["video_groups"].([]map[string]any)) > 0 || len(r["sub_groups"].([]map[string]any)) > 0 {
			results = append(results, r)
		}
		totalVideo += int(firstInt64(r, "video_dup_count"))
		totalSub += int(firstInt64(r, "sub_dup_count"))
	}
	return map[string]any{
		"libraries":       results,
		"total_video_dup": totalVideo,
		"total_sub_dup":   totalSub,
		"total_dup":       totalVideo + totalSub,
	}
}

// dedupApply: 对指定库执行删除：从库 JSON 移除勾选的 path，删除前自动备份
func (a *App) dedupApply(libID string, deletePaths []string) map[string]any {
	lib, err := a.cfg.LoadLib(libID)
	if err != nil {
		return map[string]any{"ok": false, "error": "library not found"}
	}
	files, _ := lib["files"].([]any)
	delSet := map[string]bool{}
	for _, p := range deletePaths {
		delSet[p] = true
	}
	keep := []any{}
	for _, f := range files {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if !delSet[asString(fm["path"])] {
			keep = append(keep, fm)
		}
	}
	removed := len(files) - len(keep)
	if removed == 0 {
		return map[string]any{"ok": true, "lib_id": libID, "removed": 0, "message": "无删除项"}
	}

	// 备份原库 JSON
	p := a.cfg.LibPath(libID)
	bak := fmt.Sprintf("%s.bak.%d", p, time.Now().Unix())
	if b, err := os.ReadFile(p); err == nil {
		os.WriteFile(bak, b, 0o644)
	}

	// 重建 idx 并写回
	newFiles := []any{}
	for i, f := range keep {
		fm, _ := f.(map[string]any)
		nf := map[string]any{}
		for k, v := range fm {
			nf[k] = v
		}
		nf["idx"] = i
		newFiles = append(newFiles, nf)
	}
	lib["files"] = newFiles
	a.cfg.writeLibraryFile(p, lib)
	return map[string]any{"ok": true, "lib_id": libID, "removed": removed, "message": fmt.Sprintf("已删除 %d 个重复文件", removed)}
}
