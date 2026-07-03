# 123云盘秒传JSON转STRM无限制挂载工具

[![Docker Image](https://img.shields.io/badge/docker-ready-blue)]()
[![Platform](https://img.shields.io/badge/platform-amd64%20%7C%20arm64-green)]()
[![License](https://img.shields.io/badge/license-GPL--3.0-orange)]()

一个面向 NAS / Docker 的 123 云盘媒体库 STRM 工具。

本项目可以将 123 云盘秒传 JSON 转换为 STRM 文件，并在播放时自动秒传到 123 云盘缓存目录，获取真实下载直链后 302 跳转，适合 Emby / Jellyfin / Plex / Infuse / VidHub 等媒体库使用。

## 项目特点

- 秒传 JSON 转 STRM
- 不受 123 云盘容量限制
- Docker 一键部署
- 支持 NAS 长期运行
- 支持 302 直链播放
- 支持 Base62 ETag
- 支持字幕 STRM 生成
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
- Infuse
- VidHub

示例：

```text
/strm/电影
/strm/动漫
/strm/剧集
```

## 常见问题

### 播放时必须运行容器吗？

必须。STRM 文件指向本服务的 `/play` 接口。

### 可以用 127.0.0.1 吗？

如果播放器和服务不在同一台机器，不可以。  
NAS 部署建议使用 NAS 局域网 IP。

### 为什么不使用 WebDAV？

WebDAV 在大目录下容易卡顿，STRM 更适合媒体库扫描。

### 修改账号密码后需要删除 token 吗？

不需要，程序会自动处理。

## 鸣谢

本项目基于以下项目改造：

```text
https://github.com/realcwj/123Pan-Unlimited-WebDAV
```

秒传 JSON 可由以下项目生成：

```text
https://github.com/Bao-qing/123FastLink
```
