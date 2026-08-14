package app

import "testing"

// 归档任务生成的秒传 JSON 必须能被 NormalizeLibrary 接受(回归: []map[string]any 曾导致 unsupported format)
func TestArchiveSecJSONNormalize(t *testing.T) {
	secJSON := map[string]any{
		"scriptVersion":           "114514",
		"usesBase62EtagsInExport": true,
		"commonPath":              "电影/",
		"files": []any{
			map[string]any{"path": "电影/动作片/a.mp4", "size": int64(100), "etag": "abc123"},
			map[string]any{"path": "电影/动作片/b.mp4", "size": int64(200), "etag": "def456"},
			map[string]any{"path": "电影/没有etag.mp4", "size": int64(300)}, // 缺 etag 应被跳过
		},
	}
	lib, err := (&Config{}).NormalizeLibrary(secJSON, "电影", "电影")
	if err != nil {
		t.Fatalf("NormalizeLibrary 应接受归档 secJSON: %v", err)
	}
	files, _ := lib["files"].([]FileInfo)
	if len(files) != 2 {
		t.Fatalf("期望 2 个文件(缺 etag 的被跳过), 实际 %d", len(files))
	}
}
