package app

import (
	"encoding/json"
	"fmt"
	"math/big"
	"path"
	"regexp"
	"sort"
	"strings"
)

const BASE62_CHARS = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// truncate: 日志用，截断过长的响应内容
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// parseJSONBody: 健壮解析 123 API 响应体。
// 部分接口/中间层会在 JSON 前追加前缀（如 `true{...}` / `200 {...}`），
// json.Unmarshal 直接解析会失败；这里剥离前缀到第一个 `{`/`[` 再解析。
func parseJSONBody(b []byte) (map[string]any, error) {
	var rd map[string]any
	if err := json.Unmarshal(b, &rd); err == nil {
		return rd, nil
	}
	for i, c := range b {
		if c == '{' || c == '[' {
			var m map[string]any
			if err := json.Unmarshal(b[i:], &m); err == nil {
				return m, nil
			}
			break
		}
	}
	return nil, fmt.Errorf("invalid json response: %s", truncate(string(b), 200))
}

func isHexMD5(etag string) bool {
	m, _ := regexp.MatchString(`^[0-9a-fA-F]{32}$`, etag)
	return m
}

// encryptEtagTo123FastLinkEtag: hex -> Base62
func encryptEtagTo123FastLinkEtag(etag string) string {
	n := new(big.Int)
	n.SetString(etag, 16)
	if n.Sign() == 0 {
		return string(BASE62_CHARS[0])
	}
	base := big.NewInt(62)
	var chars []byte
	zero := big.NewInt(0)
	rem := new(big.Int)
	for n.Cmp(zero) > 0 {
		n.DivMod(n, base, rem)
		chars = append(chars, BASE62_CHARS[rem.Int64()])
	}
	for i, j := 0, len(chars)-1; i < j; i, j = i+1, j-1 {
		chars[i], chars[j] = chars[j], chars[i]
	}
	return string(chars)
}

// decrypt123FastLinkEtagToEtag: Base62 -> hex
func decrypt123FastLinkEtagToEtag(encrypted string) string {
	n := big.NewInt(0)
	base := big.NewInt(62)
	for i := 0; i < len(encrypted); i++ {
		idx := strings.IndexByte(BASE62_CHARS, encrypted[i])
		n.Mul(n, base)
		n.Add(n, big.NewInt(int64(idx)))
	}
	hexStr := fmt.Sprintf("%x", n)
	if len(hexStr) < 32 {
		hexStr = strings.Repeat("0", 32-len(hexStr)) + hexStr
	}
	return hexStr
}

func toSecEtag(etag string) string {
	etag = strings.TrimSpace(etag)
	if etag == "" {
		return ""
	}
	if isHexMD5(etag) {
		return encryptEtagTo123FastLinkEtag(etag)
	}
	return etag
}

// anonymizeId: 匿名化 FileId/parentFileId，同步修改 AbsPath
func anonymizeId(itemsList []map[string]any) []map[string]any {
	sort.SliceStable(itemsList, func(i, j int) bool {
		return fmt.Sprintf("%v", itemsList[i]["FileName"]) < fmt.Sprintf("%v", itemsList[j]["FileName"])
	})

	mapID := make(map[int64]int)
	count := 0
	for _, item := range itemsList {
		fid := toInt64(item["FileId"])
		if _, ok := mapID[fid]; !ok {
			mapID[fid] = count
			count++
		}
		pid := toInt64(item["parentFileId"])
		if _, ok := mapID[pid]; !ok {
			mapID[pid] = count
			count++
		}
	}

	result := make([]map[string]any, 0, len(itemsList))
	for _, item := range itemsList {
		absPath := strings.Split(fmt.Sprintf("%v", item["AbsPath"]), "/")
		var parts []string
		for _, seg := range absPath {
			if seg == "" {
				continue
			}
			parts = append(parts, fmt.Sprintf("%d", mapID[toInt64(seg)]))
		}
		result = append(result, map[string]any{
			"FileId":       mapID[toInt64(item["FileId"])],
			"FileName":     item["FileName"],
			"Type":         item["Type"],
			"Size":         item["Size"],
			"Etag":         item["Etag"],
			"parentFileId": mapID[toInt64(item["parentFileId"])],
			"AbsPath":      strings.Join(parts, "/"),
		})
	}
	return result
}

// makeAbsPath: 构建 AbsPath
func makeAbsPath(fullDict map[int64][]map[string]any, parentFileId int64) map[int64][]map[string]any {
	parentMapping := make(map[int64]int64)
	for key, value := range fullDict {
		for _, item := range value {
			parentMapping[toInt64(item["FileId"])] = key
		}
	}
	for _, value := range fullDict {
		for _, item := range value {
			absPath := fmt.Sprintf("%d", toInt64(item["FileId"]))
			firstSeg := func(s string) int64 {
				parts := strings.SplitN(s, "/", 2)
				return toInt64(parts[0])
			}
			for firstSeg(absPath) != parentFileId {
				pid := parentMapping[firstSeg(absPath)]
				absPath = fmt.Sprintf("%d/%s", pid, absPath)
			}
			item["AbsPath"] = absPath
		}
	}
	return fullDict
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case int32:
		return int64(t)
	case float64:
		return int64(t)
	case float32:
		return int64(t)
	case string:
		var n int64
		fmt.Sscanf(t, "%d", &n)
		return n
	case nil:
		return 0
	}
	return 0
}

func safeName(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	for _, c := range BAD_CHARS {
		s = strings.ReplaceAll(s, string(c), " ")
	}
	s = spacesRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, ".")
	if s == "" {
		return "unnamed"
	}
	return s
}

func safeRelPath(p string) string {
	var parts []string
	for _, x := range strings.Split(strings.ReplaceAll(p, "\\", "/"), "/") {
		x = strings.TrimSpace(x)
		if x == "" || x == "." || x == ".." {
			continue
		}
		parts = append(parts, safeName(x))
	}
	if len(parts) == 0 {
		return path.Clean("unnamed")
	}
	return path.Join(parts...)
}

func base62ToHexCandidates(etag string) []string {
	etag = strings.TrimSpace(etag)
	if etag == "" {
		return nil
	}
	if isHexMD5(etag) {
		return []string{strings.ToLower(etag)}
	}
	alphabets := []string{
		"0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ",
		"0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz",
		"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789",
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
	}
	var out []string
	for _, ab := range alphabets {
		n := big.NewInt(0)
		base := big.NewInt(62)
		ok := true
		for i := 0; i < len(etag); i++ {
			idx := strings.IndexByte(ab, etag[i])
			if idx < 0 {
				ok = false
				break
			}
			n.Mul(n, base)
			n.Add(n, big.NewInt(int64(idx)))
		}
		if !ok {
			continue
		}
		h := fmt.Sprintf("%032x", n)
		if len(h) > 32 {
			h = h[len(h)-32:]
		}
		dup := false
		for _, e := range out {
			if e == h {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, h)
		}
	}
	dup := false
	for _, e := range out {
		if e == etag {
			dup = true
			break
		}
	}
	if !dup {
		out = append(out, etag)
	}
	return out
}
