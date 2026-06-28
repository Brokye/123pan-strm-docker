# 123Pan STRM Docker

一个面向 NAS / Docker 部署的 123 云盘秒传 JSON 转 STRM 工具。

它不再通过 WebDAV 挂载大目录，而是：

1. 保存 123 秒传 JSON；
2. 按 JSON 原始目录结构生成 `.strm` 文件；
3. 播放 STRM 时通过 `/play/{id}/{etag}/{size}/{filename}` 自动秒传到 123 缓存目录；
4. 获取真实下载直链并 302 跳转。

适合 Emby / Jellyfin / Plex / Infuse / VidHub 等媒体库使用。

---

## 特性

- 前后端一体 FastAPI Web UI
- Docker / NAS 友好部署
- `/data` 持久化保存配置、账号、token、秒传 JSON 库
- `/strm` 持久化输出 STRM 文件
- 支持秒传 JSON 格式：

```json
{
  "commonPath": "来自：BT磁力链下载/",
  "files": [
    {
      "etag": "5p0BMpoLKsX7nCPS6tMZOb",
      "size": "13599051679",
      "path": "电影/xxx/xxx.mkv"
    }
  ]
}
```

- 支持 Base62 ETag 自动尝试转换为 32 位 MD5 hex
- 生成 STRM 内容示例：

```text
http://NAS_IP:12366/play/0/dd0417083bd4f658115b1116a823daa5/6981086608/xxx.mkv
```

---

## Docker Compose 部署

### 1. 修改 `docker-compose.yml`

把：

```yaml
SERVER_BASE=http://192.168.31.189:12366
```

改成你的 NAS 地址，例如：

```yaml
SERVER_BASE=http://192.168.1.10:8000
```

默认示例使用：

```yaml
ports:
  - "12366:8000"
```

表示：

```text
宿主机端口 12366 -> 容器端口 8000
```

如果想宿主机也使用 8000 端口：

```yaml
ports:
  - "8000:8000"
```

同时改：

```yaml
SERVER_BASE=http://192.168.1.10:8000
```

### 2. 修改 STRM 输出目录

默认：

```yaml
volumes:
  - ./data:/data
  - ./strm:/strm
```

如果要输出到 NAS 媒体库：

```yaml
volumes:
  - ./data:/data
  - /volume1/media/strm:/strm
```

### 3. 启动

```bash
docker compose up -d --build
```

### 4. 打开 Web UI

```text
http://你的NAS_IP:12366
```

或如果你映射的是 8000：

```text
http://你的NAS_IP:8000
```

---

## 使用流程

1. 打开 Web UI；
2. 填写 123 云盘账号密码并保存；
3. 上传或粘贴秒传 JSON；
4. 保存到库；
5. 设置服务地址，例如：

```text
http://192.168.1.10:12366
```

6. 设置输出目录，一般容器内为：

```text
/strm
```

7. 点击生成 STRM；
8. 把宿主机映射的 STRM 目录加入 Emby / Jellyfin 媒体库。

---

## 数据目录

```text
/data/settings.yaml       123账号密码、基础配置
/data/config.json         Web UI 配置
/data/cache.json          123 token 缓存
/data/libraries/*.json    保存的秒传 JSON 库
/strm                     生成的 STRM 文件
```

---

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `DATA_DIR` | `/data` | 持久化数据目录 |
| `SETTINGS_PATH` | `/data/settings.yaml` | settings 配置文件 |
| `CACHE_PATH` | `/data/cache.json` | 123 登录 token 缓存 |
| `STRM_OUTPUT_DIR` | `/strm` | STRM 输出目录 |
| `SERVER_BASE` | `http://127.0.0.1:8000` | 写入 STRM 的服务地址 |
| `HOST` | `0.0.0.0` | 监听地址 |
| `PORT` | `8000` | 容器内监听端口 |

---

## 注意事项

- 播放时容器必须运行；
- STRM 里的地址不要用 `127.0.0.1`，除非播放器和服务在同一台机器；
- NAS / Docker 部署时建议使用 NAS 局域网 IP；
- 首次播放会触发 123 秒传和直链获取，可能需要等待几秒；
- 如果 JSON 的 ETag 是 Base62，工具会自动尝试转换。

---

## 鸣谢

本项目基于 123Pan Unlimited WebDAV 相关逻辑整理改造，移除了 WebDAV 挂载路径，改为更适合媒体库的大规模 STRM 生成方式。
