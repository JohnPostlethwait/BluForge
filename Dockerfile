# Stage 1: Build
FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .
RUN go build -o bluforge .

# Stage 2: Runtime
FROM alpine:3.20

# Install MakeMKV dependencies and FFmpeg
RUN apk add --no-cache \
    ffmpeg \
    libstdc++ \
    libgcc \
    ca-certificates \
    wget

# Install MakeMKV
RUN apk add --no-cache --repository http://dl-cdn.alpinelinux.org/alpine/edge/testing \
    makemkv 2>/dev/null || \
    (wget -q https://www.makemkv.com/download/makemkv-bin-1.17.7.tar.gz -O /tmp/makemkv-bin.tar.gz && \
     wget -q https://www.makemkv.com/download/makemkv-oss-1.17.7.tar.gz -O /tmp/makemkv-oss.tar.gz && \
     echo "MakeMKV will be installed at runtime or provided via volume mount")

WORKDIR /app

COPY --from=builder /app/bluforge .

EXPOSE 9160

VOLUME ["/config", "/output"]

CMD ["./bluforge"]
