# 多阶段构建：golang 交叉编译静态二进制 + 精简 runtime（支持 amd64 / arm64）
# 构建阶段固定跑在宿主机架构(BUILDPLATFORM)，用 GOOS/GOARCH 交叉编译到目标平台，避免 QEMU 模拟、大幅提速。
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/123pan-strm .

FROM alpine:3.20

ENV TZ=Asia/Shanghai \
    DATA_DIR=/data \
    SETTINGS_PATH=/data/settings.yaml \
    CACHE_PATH=/data/cache.json \
    STRM_OUTPUT_DIR=/strm \
    HOST=0.0.0.0 \
    PORT=8000

WORKDIR /app

COPY --from=builder /out/123pan-strm /app/123pan-strm

EXPOSE 8000 8098

VOLUME ["/data", "/strm"]

ENTRYPOINT ["/app/123pan-strm"]
