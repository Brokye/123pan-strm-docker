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

默认使用 Docker Hub 镜像：

```text
ssabc/123pan-strm-docker:latest
```

备用 GHCR 镜像：

```text
ghcr.io/ssabv/123pan-strm-docker:latest
```

普通用户建议直接使用 Docker Hub 镜像，NAS 拉取更方便。

---

## 最简单启动方式：docker run

> 镜像已内置默认持久化路径：`DATA_DIR=/data`、`SETTINGS_PATH=/data/settings.yaml`、`CACHE_PATH=/data/cache.json`、`STRM_OUTPUT_DIR=/strm`，普通用户无需手动填写这些环境变量。


把下面的 `192.168.31.189` 改成你的 NAS IP。

```bash
docker run -d \
  --name 123pan-strm \
  --restart unless-stopped \
  -p 8000:8000 \
  -e TZ=Asia/Shanghai \
  -e SERVER_BASE=http://192.168.31.189:8000 \
  -v ./data:/data \
  -v ./strm:/strm \
  ssabc/123pan-strm-docker:latest
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


## Docker Compose 启动

新建 `docker-compose.yml`：

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

## 项目来源

本项目基于以下开源项目改造：

```text
https://github.com/realcwj/123Pan-Unlimited-WebDAV
```

原项目主要提供 123 云盘无限制 WebDAV 挂载能力。

本项目在其基础上进行了改造：

- 去掉 WebDAV 挂载大目录的使用方式；
- 改为保存秒传 JSON 并直接生成 STRM；
- 播放 STRM 时通过 `/play/{id}/{etag}/{size}/{filename}` 自动获取 123 真实直链；
- 增加 Docker / NAS 一体化部署和 Web UI。

感谢原项目作者的工作。

---

## 注意事项

> 说明：v1.0.7 起，网页登录主 API 参考 OpenList 的 123 驱动：登录使用 `https://login.123pan.com/api/user/sign_in`，文件/秒传 API 使用 `https://yun.123pan.com/b/api`，Referer/Origin 使用 `https://yun.123pan.com/`。


- 播放时容器必须保持运行；
- STRM 里的地址不要写 `127.0.0.1`，除非播放器也在同一台机器；
- NAS 部署时请写 NAS 局域网 IP；
- 首次播放会触发秒传和直链获取，可能等待几秒；
- 如果 JSON 里的 ETag 是 Base62，工具会自动尝试转换；
- 如果 123 登录失败，请在 Web UI 重新保存账号密码；
- 如果你修改了 123 账号或密码，程序会自动清理 `/data/cache.json` 中的旧 token；无需手动删除。

---

## 手动构建

如果你不想拉镜像，也可以本地构建：

```bash
git clone https://github.com/ssabv/123pan-strm-docker.git
cd 123pan-strm-docker
docker compose up -d --build
```
