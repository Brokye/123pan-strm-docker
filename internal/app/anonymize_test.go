package app

import (
	"encoding/json"
	"testing"
)

func TestAnonymizeIdCompat(t *testing.T) {
	items := []map[string]any{
		{"FileId": int64(100), "FileName": "bbb.mkv", "Type": 0, "Size": int64(10), "Etag": "abc", "parentFileId": int64(2), "AbsPath": "2/100"},
		{"FileId": int64(101), "FileName": "aaa.mp4", "Type": 0, "Size": int64(20), "Etag": "def", "parentFileId": int64(2), "AbsPath": "2/101"},
		{"FileId": int64(2), "FileName": "子目录", "Type": 1, "Size": 0, "Etag": "", "parentFileId": int64(1), "AbsPath": "2"},
		{"FileId": int64(1), "FileName": "根目录", "Type": 1, "Size": 0, "Etag": "", "parentFileId": int64(0), "AbsPath": "1"},
	}
	result := anonymizeId(items)
	b, _ := json.MarshalIndent(result, "", "  ")
	expected := `[
  {
    "AbsPath": "1/0",
    "Etag": "def",
    "FileId": 0,
    "FileName": "aaa.mp4",
    "Size": 20,
    "Type": 0,
    "parentFileId": 1
  },
  {
    "AbsPath": "1/2",
    "Etag": "abc",
    "FileId": 2,
    "FileName": "bbb.mkv",
    "Size": 10,
    "Type": 0,
    "parentFileId": 1
  },
  {
    "AbsPath": "1",
    "Etag": "",
    "FileId": 1,
    "FileName": "子目录",
    "Size": 0,
    "Type": 1,
    "parentFileId": 3
  },
  {
    "AbsPath": "3",
    "Etag": "",
    "FileId": 3,
    "FileName": "根目录",
    "Size": 0,
    "Type": 1,
    "parentFileId": 4
  }
]`
	var got, want any
	json.Unmarshal(b, &got)
	json.Unmarshal([]byte(expected), &want)
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Errorf("anonymizeId mismatch\nGO: %s\nPY: %s", string(gb), string(wb))
	}
}
