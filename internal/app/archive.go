package app

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// ArchiveJob: 定时归档任务配置（持久化到 {DATA_DIR}/jobs/*.json）
type ArchiveJob struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Enabled          bool   `json:"enabled"`
	Cron             string `json:"cron"` // 标准 5 段 cron，如 "0 3 * * *"
	PanFolderID      int64  `json:"pan_folder_id"`
	PanFolderName    string `json:"pan_folder_name"`
	Category         string `json:"category"`
	OutputDir        string `json:"output_dir"`
	ServerBase       string `json:"server_base"`
	IncludeSubtitles bool   `json:"include_subtitles"`
	DeleteAfter      bool   `json:"delete_after"`
	DeleteMode       string `json:"delete_mode"`     // trash(默认) | permanent
	DeleteStrategy   string `json:"delete_strategy"` // file(默认,只删成功项) | folder(整体删目录)
}

// ArchiveRun: 单次运行历史（持久化到 {DATA_DIR}/jobs/history/*.json）
type ArchiveRun struct {
	JobID        string   `json:"job_id"`
	JobName      string   `json:"job_name"`
	StartedAt    int64    `json:"started_at"`
	FinishedAt   int64    `json:"finished_at"`
	Status       string   `json:"status"` // ok | failed | skipped
	ScannedFiles int      `json:"scanned_files"`
	StrmsCreated int      `json:"strms_created"`
	DeletedFiles int      `json:"deleted_files"`
	FailedFiles  []string `json:"failed_files"`
	Errors       []string `json:"errors"`
}

// archiveItem: 扫描到的云盘条目
type archiveItem struct {
	FileId int64
	Name   string
	Path   string
	Size   int64
	Etag   string
	IsDir  bool
	Status string // 文件: ok | fail
}

func (a *App) archiveJobsDir() string    { return filepath.Join(a.cfg.DataDir, "jobs") }
func (a *App) archiveHistoryDir() string { return filepath.Join(a.archiveJobsDir(), "history") }

// ---------- 存储 ----------

func (a *App) LoadArchiveJobs() []ArchiveJob {
	jobs := []ArchiveJob{}
	entries, _ := os.ReadDir(a.archiveJobsDir())
	names := []string{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		d := ReadJSONFile(filepath.Join(a.archiveJobsDir(), n))
		if d == nil {
			continue
		}
		var job ArchiveJob
		if b, err := json.Marshal(d); err == nil {
			if json.Unmarshal(b, &job) == nil && job.ID != "" {
				jobs = append(jobs, job)
			}
		}
	}
	return jobs
}

func (a *App) LoadArchiveJob(id string) *ArchiveJob {
	for _, j := range a.LoadArchiveJobs() {
		if j.ID == id {
			return &j
		}
	}
	return nil
}

func (a *App) SaveArchiveJob(job ArchiveJob) (ArchiveJob, error) {
	if job.ID == "" {
		job.ID = safeName(job.Name)
		if job.ID == "" || job.ID == "unnamed" {
			job.ID = fmt.Sprintf("job_%d", time.Now().Unix())
		}
	}
	if job.DeleteMode == "" {
		job.DeleteMode = "trash"
	}
	if job.DeleteStrategy == "" {
		job.DeleteStrategy = "file"
	}
	os.MkdirAll(a.archiveJobsDir(), 0o755)
	WriteJSONFile(filepath.Join(a.archiveJobsDir(), job.ID+".json"), job)
	a.scheduleArchiveJob(job)
	return job, nil
}

func (a *App) DeleteArchiveJob(id string) {
	a.unscheduleArchiveJob(id)
	os.Remove(filepath.Join(a.archiveJobsDir(), id+".json"))
}

func (a *App) saveArchiveRun(run *ArchiveRun) {
	os.MkdirAll(a.archiveHistoryDir(), 0o755)
	WriteJSONFile(filepath.Join(a.archiveHistoryDir(), fmt.Sprintf("%s-%d.json", run.JobID, run.StartedAt)), run)
}

func (a *App) LoadArchiveRuns(jobID string, limit int) []ArchiveRun {
	runs := []ArchiveRun{}
	entries, _ := os.ReadDir(a.archiveHistoryDir())
	paths := []string{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), jobID+"-") && strings.HasSuffix(e.Name(), ".json") {
			paths = append(paths, filepath.Join(a.archiveHistoryDir(), e.Name()))
		}
	}
	sort.Slice(paths, func(i, j int) bool {
		ti, _ := os.Stat(paths[i])
		tj, _ := os.Stat(paths[j])
		return ti.ModTime().After(tj.ModTime())
	})
	if limit <= 0 {
		limit = 50
	}
	for i, p := range paths {
		if i >= limit {
			break
		}
		d := ReadJSONFile(p)
		if d == nil {
			continue
		}
		var run ArchiveRun
		if b, err := json.Marshal(d); err == nil && json.Unmarshal(b, &run) == nil {
			runs = append(runs, run)
		}
	}
	return runs
}

// ---------- 调度 ----------

func (a *App) startArchiveScheduler() {
	if a.cron != nil {
		return
	}
	a.cron = cron.New()
	a.cronEntries = map[string]cron.EntryID{}
	for _, job := range a.LoadArchiveJobs() {
		if job.Enabled && job.Cron != "" {
			a.scheduleArchiveJob(job)
		}
	}
	a.cron.Start()
	log.Printf("[归档] 定时调度器已启动，共 %d 个任务", len(a.cronEntries))
}

func (a *App) scheduleArchiveJob(job ArchiveJob) {
	a.cronMu.Lock()
	defer a.cronMu.Unlock()
	if a.cron == nil {
		return
	}
	if id, ok := a.cronEntries[job.ID]; ok {
		a.cron.Remove(id)
		delete(a.cronEntries, job.ID)
	}
	if !job.Enabled || job.Cron == "" {
		return
	}
	entryID, err := a.cron.AddFunc(job.Cron, func() {
		jobCopy := job
		taskID := a.startTask("archive_"+jobCopy.ID, func(emit func(map[string]any)) {
			a.archiveJobRun(jobCopy, emit)
		})
		log.Printf("[归档] 定时触发 %s (%s), task=%s", jobCopy.Name, jobCopy.Cron, taskID)
	})
	if err != nil {
		log.Printf("[归档] cron 表达式无效 %s(%s): %v", job.Name, job.Cron, err)
		return
	}
	a.cronEntries[job.ID] = entryID
	log.Printf("[归档] 已调度 %s: %s", job.Name, job.Cron)
}

func (a *App) unscheduleArchiveJob(id string) {
	a.cronMu.Lock()
	defer a.cronMu.Unlock()
	if a.cron == nil {
		return
	}
	if eid, ok := a.cronEntries[id]; ok {
		a.cron.Remove(eid)
		delete(a.cronEntries, id)
	}
}

// ---------- 编排 ----------

// archiveJobRun: 扫描 → 入库 → 生成 STRM → 安全闸门 → 删除云盘文件
func (a *App) archiveJobRun(job ArchiveJob, emit func(map[string]any)) {
	start := time.Now().Unix()
	run := &ArchiveRun{JobID: job.ID, JobName: job.Name, StartedAt: start, Status: "ok"}
	defer func() {
		run.FinishedAt = time.Now().Unix()
		a.saveArchiveRun(run)
	}()

	emit(map[string]any{"message": "连接云盘...", "progress": 5})
	driver, err := a.getPanDriver(false)
	if err != nil {
		run.Status = "failed"
		run.Errors = append(run.Errors, "云盘登录失败: "+err.Error())
		emit(map[string]any{"message": run.Errors[0], "progress": 5, "error": run.Errors[0]})
		return
	}

	emit(map[string]any{"message": "递归扫描云盘文件夹...", "progress": 10})
	files, folders := a.archiveScan(driver, job.PanFolderID, job.PanFolderName)
	if len(files) == 0 {
		run.Status = "skipped"
		emit(map[string]any{"message": "文件夹为空或已处理，跳过", "progress": 100,
			"result": map[string]any{"status": "skipped", "scanned": 0}})
		return
	}
	run.ScannedFiles = len(files)
	log.Printf("[归档] %s: 扫描到 %d 个文件, %d 个文件夹", job.Name, len(files), len(folders))

	// 只要目录以下: 剥离目标文件夹名前缀, 保证 files[].path 为相对路径(commonPath 之下)
	prefix := strings.TrimRight(job.PanFolderName, "/")
	if prefix != "" {
		prefix += "/"
		for i := range files {
			files[i].Path = strings.TrimPrefix(files[i].Path, prefix)
		}
		for i := range folders {
			folders[i].Path = strings.TrimPrefix(folders[i].Path, prefix)
		}
	}

	// 生成秒传 JSON 并保存为库
	emit(map[string]any{"message": "生成秒传 JSON 并保存到库...", "progress": 30})
	filesOut := []any{}
	libIdx := map[int64]int{} // FileId -> 库内 idx(NormalizeLibrary 按 filesOut 顺序重编号)
	for _, f := range files {
		if f.Etag == "" {
			continue
		}
		libIdx[f.FileId] = len(filesOut)
		filesOut = append(filesOut, map[string]any{"path": f.Path, "size": f.Size, "etag": toSecEtag(f.Etag)})
	}
	secJSON := map[string]any{
		"scriptVersion":           "114514",
		"exportVersion":           "114514",
		"usesBase62EtagsInExport": true,
		"commonPath":              job.PanFolderName + "/",
		"files":                   filesOut,
	}
	libName := job.PanFolderName
	if libName == "" {
		libName = job.Name
	}
	libName = fmt.Sprintf("%s_%s", libName, time.Now().Format("200601021504"))
	lib, err := a.cfg.NormalizeLibrary(secJSON, libName, job.Category)
	if err != nil {
		run.Status = "failed"
		run.Errors = append(run.Errors, "库格式转换失败: "+err.Error())
		emit(map[string]any{"message": run.Errors[0], "progress": 30, "error": run.Errors[0]})
		return
	}
	p := a.cfg.LibPath(asString(lib["id"]))
	if _, err := os.Stat(p); err == nil {
		lib["id"] = safeName(fmt.Sprintf("%s_%d", asString(lib["id"]), start))
		p = a.cfg.LibPath(asString(lib["id"]))
	}
	a.cfg.writeLibraryFile(p, lib)
	if _, err := os.Stat(p); err != nil {
		run.Status = "failed"
		run.Errors = append(run.Errors, "秒传库保存失败: "+p)
		emit(map[string]any{"message": run.Errors[0], "progress": 30, "error": run.Errors[0]})
		return
	}
	log.Printf("[归档] %s: 秒传库已保存 %s (%d 文件)", job.Name, asString(lib["id"]), len(filesOut))

	// 生成 STRM(内联, 记录 per-file 状态)
	emit(map[string]any{"message": "生成 STRM 文件...", "progress": 40})
	cfgMap := a.cfg.Config()
	fastMode := asString(cfgMap["mode"]) == "fast"
	// 输出目录: 留空时回退到全局配置
	outRoot := strings.TrimSpace(job.OutputDir)
	if outRoot == "" {
		outRoot = asString(cfgMap["output_dir"])
	}
	if outRoot == "" {
		outRoot = a.cfg.DefaultOutDir
	}
	outRoot = filepath.Clean(outRoot)
	if sub, ok := CATEGORY_DIRS[job.Category]; ok {
		outRoot = filepath.Join(outRoot, sub)
	}
	serverBase := job.ServerBase
	if serverBase == "" {
		serverBase = asString(cfgMap["server_base"])
	}
	if serverBase == "" {
		serverBase = a.cfg.defaultServerBase()
	}
	os.MkdirAll(outRoot, 0o755)

	okCount := 0
	var failedPaths []string
	videoOnly := 0
	var fileMu sync.Mutex
	mark := func(f *archiveItem, ok bool) {
		fileMu.Lock()
		if ok {
			f.Status = "ok"
			okCount++
		} else {
			f.Status = "fail"
			failedPaths = append(failedPaths, f.Path)
		}
		fileMu.Unlock()
	}

	// 第一阶段: 视频 STRM(并行写文件, 8 并发)
	videoOK := 0
	for i := range files {
		ext := strings.ToLower(filepath.Ext(safeRelPath(files[i].Path)))
		if VIDEO_EXTS[ext] {
			videoOnly++
		}
	}
	var vg sync.WaitGroup
	vsem := make(chan struct{}, 8)
	var progMu sync.Mutex
	done := 0
	for i := range files {
		f := &files[i]
		ext := strings.ToLower(filepath.Ext(safeRelPath(f.Path)))
		if !VIDEO_EXTS[ext] {
			continue
		}
		vg.Add(1)
		go func(f *archiveItem) {
			defer vg.Done()
			vsem <- struct{}{}
			defer func() { <-vsem }()
			rel := safeRelPath(f.Path)
			target := filepath.Join(outRoot, relWithoutSuffix(rel, ext)+".strm")
			os.MkdirAll(filepath.Dir(target), 0o755)
			url := makePlayURL(serverBase, libIdx[f.FileId], f.Etag, f.Size, filepath.Base(rel))
			if err := os.WriteFile(target, []byte(url+"\n"), 0o644); err != nil {
				mark(f, false)
				log.Printf("[归档] STRM 写入失败 %s: %v", target, err)
			} else {
				mark(f, true)
				progMu.Lock()
				videoOK++
				progMu.Unlock()
			}
			progMu.Lock()
			done++
			d := done
			progMu.Unlock()
			emit(map[string]any{"message": fmt.Sprintf("生成 STRM %d/%d...", d, videoOnly),
				"progress": 40 + int(float64(d)/float64(maxInt(videoOnly, 1))*25)})
		}(f)
	}
	vg.Wait()

	// 第二阶段: 字幕并行下载(8 并发, 与主页同步 STRM 一致)
	if job.IncludeSubtitles {
		var subs []*archiveItem
		for i := range files {
			f := &files[i]
			ext := strings.ToLower(filepath.Ext(safeRelPath(f.Path)))
			if SUBTITLE_EXTS[ext] {
				subs = append(subs, &files[i])
			}
		}
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		for _, f := range subs {
			wg.Add(1)
			go func(f *archiveItem) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				rel := safeRelPath(f.Path)
				target := filepath.Join(outRoot, rel)
				ok := a.downloadSubtitleFile(map[string]any{"path": f.Path, "etag": f.Etag, "size": f.Size}, target, fastMode)
				mark(f, ok)
				suffix := ""
				if !ok {
					suffix = " (失败)"
				}
				emit(map[string]any{"message": "下载字幕 " + f.Name + suffix, "progress": 68})
			}(f)
		}
		wg.Wait()
		emit(map[string]any{"message": "字幕下载完成", "progress": 70})
	}

	// 第三阶段: 杂项文件(非视频非字幕)无需 STRM, 直接视为可删
	for i := range files {
		f := &files[i]
		if f.Status != "" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(safeRelPath(f.Path)))
		if !VIDEO_EXTS[ext] && !(job.IncludeSubtitles && SUBTITLE_EXTS[ext]) {
			f.Status = "ok" // 无需生成, 闸门通过后一并删除
		}
	}
	run.StrmsCreated = okCount
	run.FailedFiles = failedPaths

	if !job.DeleteAfter {
		emit(map[string]any{"message": fmt.Sprintf("完成: 生成 STRM %d/%d, 未开启删除", okCount, len(files)), "progress": 100,
			"result": map[string]any{"status": "ok", "scanned": len(files), "strms": okCount, "deleted": 0, "lib_id": asString(lib["id"])}})
		return
	}

	// 安全闸门: 视频 STRM 必须全部成功(字幕成功数不得补足视频失败数)
	emit(map[string]any{"message": "校验 STRM 完整性...", "progress": 72})
	if job.DeleteStrategy == "file" && videoOK < videoOnly {
		run.Status = "failed"
		run.Errors = append(run.Errors, fmt.Sprintf("STRM 生成不完整 (%d/%d 视频), 已中止删除, 云盘文件保留", videoOK, videoOnly))
		emit(map[string]any{"message": run.Errors[0], "progress": 100, "error": run.Errors[0],
			"result": map[string]any{"status": "failed", "scanned": len(files), "strms": okCount, "deleted": 0, "failed_files": failedPaths}})
		return
	}

	// 删除云盘文件
	emit(map[string]any{"message": "删除云盘文件...", "progress": 80})
	deleted := a.archiveDelete(driver, files, folders, job)
	run.DeletedFiles = deleted
	log.Printf("[归档] %s: 完成, 删除 %d/%d 个文件", job.Name, deleted, len(files))

	emit(map[string]any{"message": fmt.Sprintf("完成: 生成 STRM %d, 删除云盘文件 %d/%d", okCount, deleted, len(files)), "progress": 100,
		"result": map[string]any{"status": "ok", "scanned": len(files), "strms": okCount, "deleted": deleted,
			"failed_files": failedPaths, "lib_id": asString(lib["id"])}})
}

// archiveScan: 并行递归扫描文件夹(BFS + goroutine 池, 与主页"盘内文件生成"同速)
func (a *App) archiveScan(driver *Pan123, folderID int64, rootName string) ([]archiveItem, []archiveItem) {
	files := []archiveItem{}
	folders := []archiveItem{}
	queue := []archiveItem{{FileId: folderID, IsDir: true, Path: ""}}
	var mu sync.Mutex
	const workers = 8
	sem := make(chan struct{}, workers)

	for len(queue) > 0 {
		batch := queue
		queue = []archiveItem{}
		var wg sync.WaitGroup
		for _, t := range batch {
			wg.Add(1)
			go func(t archiveItem) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				r := driver.listFilesSingle(t.FileId)
				var out []archiveItem
				if e, ok := r["error"]; ok {
					log.Printf("[归档] 扫描失败 %s: %v", t.Path, e)
				} else {
					for _, it := range asAnySlice(r["items"]) {
						im, _ := it.(map[string]any)
						fid := int64(asFloat(im["FileId"]))
						name := asString(im["FileName"])
						cpath := name
						if t.Path != "" {
							cpath = t.Path + "/" + name
						}
						if typ, _ := im["Type"].(float64); typ == 1 {
							out = append(out, archiveItem{FileId: fid, Name: name, Path: cpath, IsDir: true})
						} else {
							out = append(out, archiveItem{
								FileId: fid, Name: name, Path: cpath,
								Size: int64(asFloat(im["Size"])), Etag: asString(im["Etag"]),
							})
						}
					}
				}
				mu.Lock()
				for _, it := range out {
					if it.IsDir {
						folders = append(folders, it)
						queue = append(queue, it)
					} else {
						files = append(files, it)
					}
				}
				mu.Unlock()
			}(t)
		}
		wg.Wait()
	}
	return files, folders
}

// archiveDelete: 删除云盘文件。strategy=file 只删 Status==ok 的, 再清空文件夹; folder 整体删目录
func (a *App) archiveDelete(driver *Pan123, files, folders []archiveItem, job ArchiveJob) int {
	clearTrash := job.DeleteMode == "permanent"

	// 策略 folder: 直接删目标文件夹整体(含失败项, 用户显式选择)
	if job.DeleteStrategy == "folder" {
		res := driver.deleteFile([]map[string]any{{"FileId": job.PanFolderID}}, clearTrash)
		if isFinish, _ := res["isFinish"].(bool); isFinish {
			return len(files)
		}
		log.Printf("[归档] 整体删除目录失败: %v", res["message"])
		return 0
	}

	// 策略 file(默认): 只删已成功生成 STRM 的文件
	var toDel []map[string]any
	for _, f := range files {
		if f.Status == "ok" {
			toDel = append(toDel, map[string]any{"FileId": f.FileId})
		}
	}
	if len(toDel) > 0 {
		res := driver.deleteFile(toDel, clearTrash)
		if isFinish, _ := res["isFinish"].(bool); !isFinish {
			log.Printf("[归档] 批量删除文件失败(%d 个): %v", len(toDel), res["message"])
		}
	}
	// 清理空文件夹(从最深层开始, 实时检查)
	for i := len(folders) - 1; i >= 0; i-- {
		f := folders[i]
		res := driver.listFilesSingle(f.FileId)
		if e, ok := res["error"]; ok {
			log.Printf("[归档] 检查空文件夹失败 %s: %v", f.Path, e)
			continue
		}
		if items, _ := res["items"].([]any); len(items) == 0 {
			driver.deleteFile([]map[string]any{{"FileId": f.FileId}}, clearTrash)
			log.Printf("[归档] 已删除空文件夹: %s", f.Path)
		}
	}
	return len(toDel)
}
