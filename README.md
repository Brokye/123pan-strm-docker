# 123云盘秒传JSON转STRM无限制挂载工具

[![Docker Image](https://img.shields.io/badge/docker-ready-blue)]()
[![Platform](https://img.shields.io/badge/platform-amd64%20%7C%20arm64-green)]()
[![License](https://img.shields.io/badge/license-GPL--3.0-orange)]()

一个面向 NAS / Docker 的 123 云盘媒体库 STRM 工具。

本项目可以将 123 云盘秒传 JSON 转换为 STRM 文件，并在播放时自动秒传到 123 云盘缓存目录，获取真实下载直链后 302 跳转，适合 Emby / Jellyfin / Plex  等媒体库使用。

## 项目特点

- 秒传 JSON 转 STRM
- 不受 123 云盘容量限制
- Docker 一键部署
- 支持 NAS 长期运行
- 支持 302 直链播放
- 支持 Base62 ETag
- 支持字幕下载
- 支持账号密码 Web UI 配置
- 避免 WebDAV 大目录卡顿

## 工作原理

```text
秒传 JSON
  ↓
生成 STRM
  ↓
媒体库读取 STRM
  ↓
请求 /play/{id}/{etag}/{size}/{filename}
  ↓
自动秒传到 123 云盘缓存目录
  ↓
获取真实下载链接
  ↓
302 跳转播放
```

## 镜像

Docker Hub：

```text
ssabc/123pan-strm-docker:latest
```

GHCR：

```text
ghcr.io/ssabv/123pan-strm-docker:latest
```

## 部署

### Docker Run

```bash
docker run -d \
  --name 123pan-strm \
  --restart unless-stopped \
  -p 8000:8000 \
  -e SERVER_BASE=http://你的NAS_IP:8000 \
  -v /你的路径/data:/data \
  -v /你的路径/strm:/strm \
  ssabc/123pan-strm-docker:latest
```

### Docker Compose

```yaml
services:
  123pan-strm:
    image: ssabc/123pan-strm-docker:latest
    container_name: 123pan-strm
    restart: unless-stopped
    ports:
      - "8000:8000"
    environment:
      - TZ=Asia/Shanghai
      - SERVER_BASE=http://你的NAS_IP:8000
    volumes:
      - ./data:/data
      - ./strm:/strm
```

```bash
docker compose up -d
```

## 配置说明

| 配置 | 说明 |
|---|---|
| `SERVER_BASE` | 写入 STRM 的服务地址 |
| `/data` | 持久化配置和 JSON 库 |
| `/strm` | STRM 输出目录 |

## Web UI 使用

访问：

```text
http://你的NAS_IP:8000
```

然后：

1. 设置 123 账号密码
2. 设置服务地址
3. 设置 STRM 输出目录
4. 上传秒传 JSON
5. 保存到库
6. 生成 STRM

## 同步下载附属文件

在「基础设置」中可配置，生成 / 同步 STRM 时把 JSON 里的附属文件一并下载到对应本地文件夹：

| 配置 | 说明 |
|---|---|
| 开启下载 | 是否在生成 STRM 时同步下载附属文件 |
| 字幕 | 下载 `.srt` / `.ass` / `.ssa` / `.vtt` / `.sub` / `.sup` |
| 元数据 NFO | 下载 `.nfo` |
| 图片 | 下载 `.jpg` / `.jpeg` / `.png` / `.webp` / `.gif` / `.bmp` / `.tbn` |
| 下载线程数 | 并发下载线程数（1–32，默认 4） |
| 失败重试次数 | 单文件下载失败后的重试次数（默认 3） |

- 附属文件会按 JSON 中的相对路径下载到 STRM 输出目录对应位置（与视频同目录）。
- 同步时会自动清理输出目录中「已启用类型」的多余附属文件；未启用的类型不会被删除。
- 下载依赖 123 网盘登录态，未登录时自动跳过。

## 秒传 JSON 来源

秒传 JSON 可以使用：

```text
https://github.com/Bao-qing/123FastLink
```

生成。

测试 JSON：

```text
https://123pan.cn/s/c42ZVv-4Zep3
```

## 媒体库配置

将生成的 STRM 目录添加到：

- Emby
- Jellyfin
- Plex

示例：

```text
/strm/电影
/strm/动漫
/strm/剧集
```

## Emby 反向代理（302 直连播放）

默认情况下 Emby 会用自己的服务端去拉取 STRM 里的直链再转给客户端（服务端中转下载）。开启反向代理后，直连播放时本服务会直接 302 跳转到 123 云盘 CDN，客户端直接从 CDN 拉流，绕过 Emby 服务端代理。

在「基础设置 → Emby 反向代理」中配置：

| 配置 | 说明 |
|---|---|
| 开启反向代理 | 是否启用（可随时开关） |
| Emby 地址 | Emby 服务地址，如 `http://127.0.0.1:8096` |
| Emby API Key | Emby 控制台 → 高级 → API 密钥（用于查询条目直链） |
| 反向代理端口 | 反向代理监听端口（默认 `8098`） |

使用方法：

1. 在容器映射端口 `8098`（见 docker-compose）。
2. 把 Emby 客户端（App / 网页）的服务器地址改为 `http://你的NAS_IP:8098`。
3. 直连播放走 302 → CDN；需要转码的请求会自动回退为普通代理，保证兼容。

## 常见问题

### 播放时必须运行容器吗？

必须。STRM 文件指向本服务的 `/play` 接口。

### 可以用 127.0.0.1 吗？

如果播放器和服务不在同一台机器，不可以。  
NAS 部署建议使用 NAS 局域网 IP。

### 为什么不使用 WebDAV？

WebDAV 在大目录下容易卡顿，STRM 更适合媒体库扫描。

## 鸣谢

本项目基于以下项目改造：

```text
https://github.com/realcwj/123Pan-Unlimited-WebDAV
```

秒传 JSON 可由以下项目生成：

```text
https://github.com/Bao-qing/123FastLink
```
