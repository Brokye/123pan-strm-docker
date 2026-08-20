package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"
)

var errUnsupportedFormat = errors.New("unsupported format")

var (
	indexHTMLBytes    []byte
	settingsYAMLBytes []byte
	archiveHTMLBytes  []byte
)

func SetEmbedded(indexHTML, settingsYAML []byte) {
	indexHTMLBytes = indexHTML
	settingsYAMLBytes = settingsYAML
}

func SetArchiveEmbedded(b []byte) {
	archiveHTMLBytes = b
}

func LoadYAMLMap(p string) map[string]any {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	m := map[string]any{}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// settingsYAMLOrder: settings.yaml 固定字段顺序（对应原版 sort_keys=False 输出）
var settingsYAMLOrder = []string{
	"DATABASE_PATH",
	"WEBDAV_USERNAME",
	"WEBDAV_PASSWORD",
	"WEBDAV_HOST",
	"WEBDAV_PORT",
	"123PAN_USERNAME",
	"123PAN_PASSWORD",
	"CACHE_FOLDER_ID",
	"SPLIT_FOLDER",
}

func WriteYAMLFile(p string, m map[string]any) {
	node := &yaml.Node{Kind: yaml.MappingNode}
	written := map[string]bool{}
	// 先按固定顺序输出已知字段
	for _, key := range settingsYAMLOrder {
		v, ok := m[key]
		if !ok {
			continue
		}
		kn := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
		vn, err := toYAMLNode(v)
		if err != nil {
			continue
		}
		node.Content = append(node.Content, kn, vn)
		written[key] = true
	}
	// 输出其余未知字段（按字母序）
	var rest []string
	for k := range m {
		if !written[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, key := range rest {
		kn := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
		vn, err := toYAMLNode(m[key])
		if err != nil {
			continue
		}
		node.Content = append(node.Content, kn, vn)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err == nil {
		os.WriteFile(p, buf.Bytes(), 0o644)
	}
	enc.Close()
}

func toYAMLNode(v any) (*yaml.Node, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		enc.Close()
		return nil, err
	}
	enc.Close()
	var node yaml.Node
	if err := yaml.Unmarshal(buf.Bytes(), &node); err != nil {
		return nil, err
	}
	if len(node.Content) > 0 {
		return node.Content[0], nil
	}
	return nil, errors.New("empty")
}

func ReadJSONFile(p string) map[string]any {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

func ReadJSONList(p string) []any {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var l []any
	if err := json.Unmarshal(b, &l); err != nil {
		return nil
	}
	return l
}

func WriteJSONFile(p string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(p, b, 0o644)
}

func strconvItoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

func parseInt(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
