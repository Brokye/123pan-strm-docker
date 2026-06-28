# 123Pan STRM Docker

把 **123 云盘秒传 JSON** 转成媒体库可识别的 **STRM 文件**。

适合在 NAS / Docker 上部署，配合 Emby、Jellyfin、Plex、Infuse、VidHub 等使用。

---

## 它是干什么的？

你有一个秒传 JSON，例如：

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

本工具可以：

1. 保存这个 JSON；
2. 按 JSON 里的目录结构生成 `.strm`；
3. 播放 STRM 时自动秒传到你的 123 云盘缓存目录；
4. 获取真实下载直链；
5. 302 跳转播放。

不走 WebDAV，所以不会因为大目录 PROPFIND 卡死。

---

## 秒传 JSON 从哪里来？

秒传 JSON 文件可以使用 **123FastLink** 生成。

测试 JSON 文件链接：

```text
https://123pan.cn/s/c42ZVv-4Zep3
```

下载或生成 JSON 后，在本工具 Web UI 中选择 JSON 文件或粘贴 JSON 内容，然后保存到库即可生成 STRM。

## 镜像地址

```text
ghcr.io/ssabv/123pan-strm-docker:latest
```

> 如果拉取提示没有权限，请到 GitHub Packages 页面把该镜像设为 Public，或先执行 `docker login ghcr.io`。

---

## 最简单启动方式：docker run

把下面的 `192.168.31.189` 改成你的 NAS IP。

```bash
docker run -d \
  --name 123pan-strm \
  --restart unless-stopped \
  -p 8000:8000 \
  -e TZ=Asia/Shanghai \
  -e SERVER_BASE=http://192.168.31.189:8000 \
  -e DATA_DIR=/data \
  -e SETTINGS_PATH=/data/settings.yaml \
  -e CACHE_PATH=/data/cache.json \
  -e STRM_OUTPUT_DIR=/strm \
  -v ./data:/data \
  -v ./strm:/strm \
  ghcr.io/ssabv/123pan-strm-docker:latest
```

然后打开：

```text
http://你的NAS_IP:8000
```

例如：

```text
http://192.168.31.189:8000
```

---

## Docker Hub 镜像

如果仓库已配置 Docker Hub 自动构建，用户也可以直接拉取 Docker Hub 镜像：

```bash
docker pull DOCKERHUB_USERNAME/123pan-strm-docker:latest
```

运行示例：

```bash
docker run -d \
  --name 123pan-strm \
  --restart unless-stopped \
  -p 8000:8000 \
  -e TZ=Asia/Shanghai \
  -e SERVER_BASE=http://你的NAS_IP:8000 \
  -e DATA_DIR=/data \
  -e SETTINGS_PATH=/data/settings.yaml \
  -e CACHE_PATH=/data/cache.json \
  -e STRM_OUTPUT_DIR=/strm \
  -v ./data:/data \
  -v ./strm:/strm \
  DOCKERHUB_USERNAME/123pan-strm-docker:latest
```

维护者启用 Docker Hub 自动构建需要在 GitHub 仓库设置中添加：

- `Secrets`：`DOCKERHUB_USERNAME`
- `Secrets`：`DOCKERHUB_TOKEN`
- `Variables`：`ENABLE_DOCKERHUB=true`

## Docker Compose 启动

新建 `docker-compose.yml`：

```yaml
services:
  123pan-strm:
    image: ghcr.io/ssabv/123pan-strm-docker:latest
    container_name: 123pan-strm
    restart: unless-stopped
    ports:
      - "8000:8000"
    environment:
      - TZ=Asia/Shanghai
      - DATA_DIR=/data
      - SETTINGS_PATH=/data/settings.yaml
      - CACHE_PATH=/data/cache.json
      - STRM_OUTPUT_DIR=/strm
      - SERVER_BASE=http://192.168.31.189:8000
      - HOST=0.0.0.0
      - PORT=8000
    volumes:
      - ./data:/data
      - ./strm:/strm
```

启动：

```bash
docker compose up -d
```

查看日志：

```bash
docker logs -f 123pan-strm
```

---

## 端口怎么理解？

```yaml
ports:
  - "8000:8000"
```

意思是：

```text
NAS外部访问端口 8000 -> 容器内部端口 8000
```

如果你想直接用 8000：

```yaml
ports:
  - "8000:8000"
```

同时把：

```yaml
SERVER_BASE=http://192.168.31.189:8000
```

改成：

```yaml
SERVER_BASE=http://192.168.31.189:8000
```

---

## Web UI 使用流程

1. 浏览器打开：

```text
http://NAS_IP:8000
```

2. 填写 123 云盘账号和密码；
3. 点击保存设置；
4. 上传或粘贴秒传 JSON；
5. 点击保存到库；
6. 设置输出目录，Docker 内通常是：

```text
/strm
```

7. 设置服务地址，例如：

```text
http://192.168.31.189:8000
```

8. 点击生成 STRM；
9. 把宿主机的 `./strm` 或你映射的媒体目录添加到 Emby/Jellyfin。

---

## STRM 内容格式

生成的 STRM 类似：

```text
http://192.168.31.189:8000/play/0/dd0417083bd4f658115b1116a823daa5/6981086608/xxx.mkv
```

最后保留原文件后缀，例如：

```text
.mkv
.mp4
.ts
```

---

## 持久化目录

| 容器目录 | 用途 |
|---|---|
| `/data` | 保存配置、账号、token、秒传 JSON 库 |
| `/strm` | 输出 STRM 文件 |

本地目录示例：

```text
./data/settings.yaml       123账号密码
./data/config.json         Web UI 配置
./data/cache.json          123 token 缓存
./data/libraries/*.json    保存的秒传 JSON 库
./strm                     生成的 STRM 文件
```

---

## NAS 媒体库映射示例

如果你想把 STRM 直接输出到 NAS 媒体库，例如：

```text
/volume1/media/strm
```

compose 里改成：

```yaml
volumes:
  - ./data:/data
  - /volume1/media/strm:/strm
```

然后 Emby/Jellyfin 添加：

```text
/volume1/media/strm
```

---

## 注意事项

- 播放时容器必须保持运行；
- STRM 里的地址不要写 `127.0.0.1`，除非播放器也在同一台机器；
- NAS 部署时请写 NAS 局域网 IP；
- 首次播放会触发秒传和直链获取，可能等待几秒；
- 如果 JSON 里的 ETag 是 Base62，工具会自动尝试转换；
- 如果 123 登录失败，请在 Web UI 重新保存账号密码；
- **如果你修改了 123 账号或密码，请删除 `/data/cache.json` 后重启容器**，否则旧 token 可能还会继续被使用。

---

## 手动构建

如果你不想拉镜像，也可以本地构建：

```bash
git clone https://github.com/ssabv/123pan-strm-docker.git
cd 123pan-strm-docker
docker compose up -d --build
```
