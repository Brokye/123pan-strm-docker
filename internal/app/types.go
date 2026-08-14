package app

import "regexp"

var VIDEO_EXTS = map[string]bool{
	".mp4": true, ".mkv": true, ".ts": true, ".m2ts": true,
	".avi": true, ".mov": true, ".wmv": true, ".flv": true,
	".rmvb": true, ".webm": true, ".mpg": true, ".mpeg": true,
	".iso": true,
}

var SUBTITLE_EXTS = map[string]bool{
	".srt": true, ".ass": true, ".ssa": true, ".vtt": true,
	".sub": true, ".sup": true,
}

const BAD_CHARS = `<>:"/\\|?*`

var CATEGORY_DIRS = map[string]string{
	"电影":  "电影",
	"剧集":  "剧集",
	"动漫":  "动漫",
	"纪录片": "纪录片",
	"综艺":  "综艺",
	"定时归档": "定时归档",
}

var spacesRe = regexp.MustCompile(`\s+`)

type FileInfo struct {
	Idx  int    `json:"idx"`
	Path string `json:"path"`
	Etag string `json:"etag"`
	Size int64  `json:"size"`
}

type Library struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CommonPath string     `json:"commonPath"`
	CreatedAt  int64      `json:"createdAt"`
	Meta       any        `json:"meta"`
	Files      []FileInfo `json:"files"`
	Category   string     `json:"category"`
}

type LibraryRow struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Total     int    `json:"total"`
	Video     int    `json:"video"`
	CreatedAt any    `json:"createdAt"`
	Category  string `json:"category"`
}

type SaveReq struct {
	Name     string `json:"name"`
	Content  any    `json:"content"`
	Category string `json:"category"`
}

type ModeReq struct {
	Mode string `json:"mode"`
}

type GenReq struct {
	LibID            string `json:"lib_id"`
	OutputDir        string `json:"output_dir"`
	ServerBase       string `json:"server_base"`
	IncludeSubtitles bool   `json:"include_subtitles"`
}

type ConfigReq struct {
	OutputDir        string `json:"output_dir"`
	ServerBase       string `json:"server_base"`
	IncludeSubtitles bool   `json:"include_subtitles"`
	PanUsername      string `json:"pan_username"`
	PanPassword      string `json:"pan_password"`
}

type UpdateLibReq struct {
	Name       string  `json:"name"`
	Category   string  `json:"category"`
	Files      any     `json:"files"`
	CommonPath *string `json:"commonPath"`
}

type PanExportReq struct {
	Folders []map[string]any `json:"folders"`
	Files   []map[string]any `json:"files"`
}

type DedupReq struct {
	LibID       string   `json:"lib_id"`
	DeletePaths []string `json:"delete_paths"`
}

type TaskState struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	State    string `json:"state"`
	Progress int    `json:"progress"`
	Message  string `json:"message"`
	Result   any    `json:"result"`
	Error    string `json:"error"`
	Updated  int64  `json:"updated"`
}
