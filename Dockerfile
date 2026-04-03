# Stage 1: Build Go app
FROM golang:1.25-alpine AS go-builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /build
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true
COPY . .
RUN go install github.com/a-h/templ/cmd/templ@v0.3.1001 && templ generate
RUN CGO_ENABLED=0 go build -o bluforge .

# Stage 2: Runtime
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

ARG MAKEMKV_VERSION=1.18.3
ENV MAKEMKV_VERSION=${MAKEMKV_VERSION}

# Runtime dependencies + build tools required for MakeMKV compilation at first start.
# libcurl4t64: makemkvcon loads libcurl via dlopen() at runtime for SDF/LibreDrive
#              data downloads — it does NOT appear in ldd output.
# zlib1g:      compression library needed by libcurl and makemkvcon.
# lsscsi:      drive detection via SCSI bus scanning.
# build-essential, pkg-config, wget, *-dev: required to compile MakeMKV from source
#              at container startup (see entrypoint.sh).
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg ca-certificates \
    libssl3 libexpat1 \
    libcurl4t64 zlib1g lsscsi \
    gosu \
    build-essential pkg-config wget \
    libc6-dev libssl-dev libexpat1-dev zlib1g-dev \
    libavcodec-dev libavutil-dev libavformat-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=go-builder /build/bluforge .
COPY --from=go-builder /build/static ./static

COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

EXPOSE 9160

VOLUME ["/config", "/output"]

ENTRYPOINT ["/app/entrypoint.sh"]
