package app

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	loginAPI = "https://login.123pan.com/api"
	mainAPI  = "https://yun.123pan.com/b/api"
)

func getActionUrl(actionName string) string {
	apis := map[string]string{
		"SignIn":           loginAPI + "/user/sign_in",
		"Logout":           mainAPI + "/user/logout",
		"UserInfo":         mainAPI + "/user/info",
		"FileList":         mainAPI + "/file/list/new",
		"DownloadInfo":     mainAPI + "/file/download_info",
		"Mkdir":            mainAPI + "/file/upload_request",
		"Move":             mainAPI + "/file/mod_pid",
		"Rename":           mainAPI + "/file/rename",
		"Trash":            mainAPI + "/file/trash",
		"UploadRequest":    mainAPI + "/file/upload_request",
		"UploadComplete":   mainAPI + "/file/upload_complete",
		"S3PreSignedUrls":  mainAPI + "/file/s3_repare_upload_parts_batch",
		"S3Auth":           mainAPI + "/file/s3_upload_object/auth",
		"UploadCompleteV2": mainAPI + "/file/upload_complete/v2",
		"S3Complete":       mainAPI + "/file/s3_complete_multipart_upload",
		"ShareList":        mainAPI + "/share/get",
		"TrashDelete":      mainAPI + "/file/delete",
	}
	return apis[actionName]
}

type Pan123 struct {
	accessToken string
	username    string
	password    string
	headers     map[string]string
	client      *http.Client
}

func NewPan123() *Pan123 {
	return &Pan123{
		headers: map[string]string{
			"origin":        "https://yun.123pan.com",
			"referer":       "https://yun.123pan.com/",
			"authorization": "",
			"user-agent":    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36",
			"platform":      "web",
			"app-version":   "3",
		},
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (p *Pan123) sleepTime() time.Duration {
	r := rand.Float64()*0.8 + 0.06
	return time.Duration(r * float64(time.Second))
}

func (p *Pan123) setAccessToken(token string) {
	p.accessToken = token
	p.headers["authorization"] = "Bearer " + token
}

func (p *Pan123) getAccessToken() string {
	return p.accessToken
}

func (p *Pan123) doLogin(username, password string) bool {
	p.username = username
	p.password = password
	var payload map[string]any
	if strings.Contains(username, "@") && strings.Contains(strings.Split(username, "@")[1], ".") {
		payload = map[string]any{"mail": username, "password": password, "type": 2}
	} else {
		payload = map[string]any{"passport": username, "password": password, "remember": true}
	}
	headers := map[string]string{
		"origin":      "https://yun.123pan.com",
		"referer":     "https://yun.123pan.com/",
		"user-agent":  p.headers["user-agent"],
		"platform":    "web",
		"app-version": "3",
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", getActionUrl("SignIn"), strings.NewReader(string(body)))
	if err != nil {
		return false
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		p.accessToken = ""
		return false
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	rd, err := parseJSONBody(data)
	if err != nil {
		log.Printf("[123] 登录响应解析失败: %s", err)
		return false
	}
	var token string
	if dt, ok := rd["data"].(map[string]any); ok {
		token, _ = dt["token"].(string)
	}
	if token != "" {
		p.accessToken = token
		p.headers["authorization"] = "Bearer " + token
		log.Printf("[123] 登录成功")
		return true
	}
	log.Printf("[123] 登录失败: %s", truncate(string(data), 200))
	p.accessToken = ""
	return false
}

// requestJSON: 统一请求封装，token 失效(code=401)自动重登一次
func (p *Pan123) requestJSON(method, actionURL string, params url.Values, jsonBody any) (map[string]any, error) {
	data, err := p.doRequest(method, actionURL, params, jsonBody)
	if err != nil {
		return nil, err
	}
	if code, _ := data["code"].(float64); code == 401 && p.username != "" && p.password != "" {
		if p.doLogin(p.username, p.password) {
			data, err = p.doRequest(method, actionURL, params, jsonBody)
			if err != nil {
				return nil, err
			}
		}
	}
	return data, nil
}

func (p *Pan123) doRequest(method, actionURL string, params url.Values, jsonBody any) (map[string]any, error) {
	var req *http.Request
	var err error
	if jsonBody != nil {
		b, _ := json.Marshal(jsonBody)
		req, err = http.NewRequest(method, actionURL, strings.NewReader(string(b)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequest(method, actionURL, nil)
		if err != nil {
			return nil, err
		}
	}
	if params != nil {
		req.URL.RawQuery = params.Encode()
	}
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	rd, err := parseJSONBody(b)
	if err != nil {
		log.Printf("[123] API 响应解析失败 %s %s: %s", method, actionURL, err)
		return nil, err
	}
	return rd, nil
}

// listFilesSingle: 单层列出（非递归，支持分页）
func (p *Pan123) listFilesSingle(parentFileId int64) map[string]any {
	body := map[string]any{
		"driveId":              "0",
		"limit":                "100",
		"next":                 "0",
		"orderBy":              "file_id",
		"orderDirection":       "desc",
		"parentFileId":         fmt.Sprintf("%d", parentFileId),
		"trashed":              "false",
		"SearchData":           "",
		"Page":                 nil,
		"OnlyLookAbnormalFile": "0",
		"event":                "homeListFile",
		"operateType":          "4",
		"inDirectSpace":        "false",
	}
	allItems := []any{}
	for {
		page := 0
		if v, ok := body["_page"]; ok {
			page = v.(int)
		}
		page++
		body["Page"] = fmt.Sprintf("%d", page)
		body["_page"] = page
		time.Sleep(p.sleepTime())
		params := url.Values{}
		for k, v := range body {
			if k == "_page" {
				continue
			}
			params.Set(k, fmt.Sprintf("%v", v))
		}
		rd, err := p.requestJSON("GET", getActionUrl("FileList"), params, nil)
		if err != nil {
			return map[string]any{"error": "请求异常: " + err.Error()}
		}
		code, _ := rd["code"].(float64)
		if code == 0 {
			dt, _ := rd["data"].(map[string]any)
			if dt == nil {
				dt = map[string]any{}
			}
			infoList, _ := dt["InfoList"].([]any)
			if infoList == nil {
				infoList = []any{}
			}
			allItems = append(allItems, infoList...)
			next, _ := dt["Next"].(string)
			if next == "-1" || len(infoList) == 0 {
				break
			}
		} else if code == 401 && p.username != "" && p.password != "" {
			if p.doLogin(p.username, p.password) {
				body["_page"] = 0
				allItems = []any{}
				continue
			}
			return map[string]any{"error": "登录状态失效，请检查账号密码"}
		} else {
			msg, _ := rd["message"].(string)
			if msg == "" {
				msg = fmt.Sprintf("code=%v", rd["code"])
			}
			return map[string]any{"error": msg}
		}
	}
	return map[string]any{"items": allItems}
}

// listFilesWithRetry: listFilesSingle + 限流自动退避重试。
// 123 个人盘接口频控严格(100011 请勿频繁操作),批量扫描时必须带重试
func (p *Pan123) listFilesWithRetry(parentFileId int64) map[string]any {
	for attempt := 0; attempt < 5; attempt++ {
		res := p.listFilesSingle(parentFileId)
		if e, ok := res["error"]; ok {
			msg := asString(e)
			if strings.Contains(msg, "频繁") || strings.Contains(msg, "稍后再试") {
				backoff := 5 * (attempt + 1)
				log.Printf("[123] 列表请求被限流(%s)，%d 秒后重试(%d/5)", msg, backoff, attempt+1)
				time.Sleep(time.Duration(backoff) * time.Second)
				continue
			}
		}
		return res
	}
	return map[string]any{"error": "重试 5 次仍被限流，请稍后再试"}
}

func (p *Pan123) createFolder(parentFileId int64, folderName string, rawData bool) map[string]any {
	folderName = strings.NewReplacer(
		":", "：", "/", "／", "\\", "＼", "*", "＊", "?", "？",
	).Replace(folderName)
	body := map[string]any{
		"driveId":      0,
		"etag":         "",
		"fileName":     folderName,
		"parentFileId": parentFileId,
		"size":         0,
		"type":         1,
	}
	rd, err := p.requestJSON("POST", getActionUrl("Mkdir"), nil, body)
	if err != nil {
		return map[string]any{"isFinish": false, "message": "创建文件夹请求发生异常: " + err.Error()}
	}
	code, _ := rd["code"].(float64)
	if code == 0 {
		if rawData {
			return map[string]any{"isFinish": true, "message": rd["data"]}
		}
		info, _ := rd["data"].(map[string]any)["Info"].(map[string]any)
		fileId, _ := info["FileId"].(float64)
		log.Printf("[123] 创建文件夹成功: %s, fileId=%d", folderName, int64(fileId))
		return map[string]any{"isFinish": true, "message": int64(fileId)}
	}
	// 同名冲突(5060)：同目录下若已存在同名文件夹则直接复用其 FileId，避免创建被拒
	if code == 5060 {
		if fid := p.findSameNameFolder(parentFileId, folderName); fid > 0 {
			log.Printf("[123] 同名文件夹已存在，直接复用: %s, fileId=%d", folderName, fid)
			if rawData {
				return map[string]any{"isFinish": true, "message": map[string]any{
					"Info": map[string]any{"FileId": float64(fid)},
				}}
			}
			return map[string]any{"isFinish": true, "message": fid}
		}
	}
	b, _ := json.Marshal(rd)
	log.Printf("[123] 创建文件夹失败: %s: %s", folderName, truncate(string(b), 200))
	return map[string]any{"isFinish": false, "message": "创建文件夹失败：" + string(b)}
}

// findSameNameFolder: 在 parentFileId 目录下查找同名文件夹，返回其 FileId；不存在或查询失败返回 0
func (p *Pan123) findSameNameFolder(parentFileId int64, folderName string) int64 {
	res := p.listFilesSingle(parentFileId)
	if errMsg, ok := res["error"].(string); ok && errMsg != "" {
		log.Printf("[123] 查询同名文件夹失败: %s", errMsg)
		return 0
	}
	items, _ := res["items"].([]any)
	for _, it := range items {
		info, _ := it.(map[string]any)
		if info == nil {
			continue
		}
		name, _ := info["FileName"].(string)
		typ := int64(asFloat(info["Type"]))
		if name == folderName && typ == 1 {
			return int64(asFloat(info["FileId"]))
		}
	}
	return 0
}

func (p *Pan123) uploadFile(etag, fileName string, parentFileId int64, size int64, rawData bool) map[string]any {
	body := map[string]any{
		"driveId":      0,
		"etag":         etag,
		"fileName":     fileName,
		"parentFileId": parentFileId,
		"size":         size,
		"type":         0,
		"duplicate":    2,
	}
	rd, err := p.requestJSON("POST", getActionUrl("UploadRequest"), nil, body)
	if err != nil {
		return map[string]any{"isFinish": false, "message": "上传文件请求发生异常: " + err.Error()}
	}
	if code, _ := rd["code"].(float64); code == 0 {
		if rawData {
			return map[string]any{"isFinish": true, "message": rd["data"]}
		}
		info, _ := rd["data"].(map[string]any)["Info"].(map[string]any)
		fileId, _ := info["FileId"].(float64)
		log.Printf("[123] 秒传上传成功: %s, fileId=%d", fileName, int64(fileId))
		return map[string]any{"isFinish": true, "message": int64(fileId)}
	}
	b, _ := json.Marshal(rd)
	log.Printf("[123] 秒传上传失败: %s: %s", fileName, truncate(string(b), 200))
	return map[string]any{"isFinish": false, "message": "上传文件失败：" + string(b)}
}

func (p *Pan123) downloadFile(etag string, fileId int64, s3keyFlag string, typ int64, fileName string, size int64) map[string]any {
	body := map[string]any{
		"driveId":   0,
		"etag":      etag,
		"fileId":    fileId,
		"s3keyFlag": s3keyFlag,
		"type":      typ,
		"fileName":  fileName,
		"size":      size,
	}
	rd, err := p.requestJSON("POST", getActionUrl("DownloadInfo"), nil, body)
	if err != nil {
		return map[string]any{"isFinish": false, "message": "获取文件下载链接请求发生异常: " + err.Error()}
	}
	if code, _ := rd["code"].(float64); code == 0 {
		dt, _ := rd["data"].(map[string]any)
		url, _ := dt["DownloadUrl"].(string)
		return map[string]any{"isFinish": true, "message": url}
	}
	b, _ := json.Marshal(rd)
	log.Printf("[123] 获取下载链接失败: %s: %s", fileName, truncate(string(b), 200))
	return map[string]any{"isFinish": false, "message": "获取文件下载链接失败：" + string(b)}
}

func (p *Pan123) deleteFile(fileList []map[string]any, clearTrash bool) map[string]any {
	trashBody := map[string]any{
		"driveId":           0,
		"event":             "intoRecycle",
		"operatePlace":      1,
		"operation":         true,
		"fileTrashInfoList": fileList,
	}
	rd, err := p.requestJSON("POST", getActionUrl("Trash"), nil, trashBody)
	if err != nil {
		return map[string]any{"isFinish": false, "message": "删除文件请求发生异常: " + err.Error()}
	}
	if code, _ := rd["code"].(float64); code == 0 {
		if !clearTrash {
			log.Printf("[123] 移入回收站成功: %d 个文件", len(fileList))
			return map[string]any{"isFinish": true, "message": "删除文件成功"}
		}
		deleteIdList := []map[string]any{}
		for _, f := range fileList {
			deleteIdList = append(deleteIdList, map[string]any{"fileId": f["FileId"]})
		}
		deleteBody := map[string]any{
			"fileIdList":    deleteIdList,
			"event":         "recycleDelete",
			"operatePlace":  1,
			"RequestSource": nil,
		}
		rd2, err := p.requestJSON("POST", getActionUrl("TrashDelete"), nil, deleteBody)
		if err != nil {
			return map[string]any{"isFinish": false, "message": "彻底删除文件请求发生异常: " + err.Error()}
		}
		if code2, _ := rd2["code"].(float64); code2 == 7301 {
			log.Printf("[123] 彻底删除成功: %d 个文件", len(fileList))
			return map[string]any{"isFinish": true, "message": "彻底删除文件成功"}
		}
		b, _ := json.Marshal(rd2)
		log.Printf("[123] 彻底删除失败: %s", truncate(string(b), 200))
		return map[string]any{"isFinish": false, "message": "彻底删除文件失败：" + string(b)}
	}
	b, _ := json.Marshal(rd)
	log.Printf("[123] 删除文件失败: %s", truncate(string(b), 200))
	return map[string]any{"isFinish": false, "message": "删除文件失败：" + string(b)}
}
