# 多阶段构建：golang 编译静态二进制 + 精简 runtime
FROM golang:1.25-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/123pan-strm .

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

RUN mkdir -p /data /strm

EXPOSE 8000

VOLUME ["/data", "/strm"]

ENTRYPOINT ["/app/123pan-strm"]
