# Stage 1: Build Go app
FROM golang:1.25-alpine AS go-builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /build
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true
COPY . .
RUN go install github.com/a-h/templ/cmd/templ@v0.3.1001 && templ generate
RUN CGO_ENABLED=0 go build -o bluforge .

# Stage 2: Build MakeMKV
FROM ubuntu:24.04 AS makemkv-builder

ARG MAKEMKV_VERSION=1.18.3
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential pkg-config wget ca-certificates \
    libc6-dev libssl-dev libexpat1-dev zlib1g-dev \
    libavcodec-dev libavutil-dev libavformat-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /tmp/makemkv

RUN wget -q "https://www.makemkv.com/download/makemkv-oss-${MAKEMKV_VERSION}.tar.gz" && \
    wget -q "https://www.makemkv.com/download/makemkv-bin-${MAKEMKV_VERSION}.tar.gz" && \
    tar xf "makemkv-oss-${MAKEMKV_VERSION}.tar.gz" && \
    tar xf "makemkv-bin-${MAKEMKV_VERSION}.tar.gz"

# Build and install the open-source library (libdriveio, libmakemkv)
RUN cd "makemkv-oss-${MAKEMKV_VERSION}" && \
    ./configure --disable-gui && make && make install

# Accept EULA and install makemkvcon binary
RUN mkdir -p "makemkv-bin-${MAKEMKV_VERSION}/tmp" && \
    echo "accepted" > "makemkv-bin-${MAKEMKV_VERSION}/tmp/eula_accepted" && \
    cd "makemkv-bin-${MAKEMKV_VERSION}" && \
    make install

# Stage 3: Runtime
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

# libcurl4t64: makemkvcon loads libcurl via dlopen() at runtime for SDF/LibreDrive
#              data downloads — it does NOT appear in ldd output.
# zlib1g:      compression library needed by libcurl and makemkvcon.
# lsscsi:      drive detection via SCSI bus scanning.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg ca-certificates \
    libssl3 libexpat1 \
    libcurl4t64 zlib1g lsscsi \
    gosu \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=go-builder /build/bluforge .
COPY --from=go-builder /build/static ./static
COPY --from=makemkv-builder /usr/bin/makemkvcon /usr/bin/makemkvcon
COPY --from=makemkv-builder /usr/lib/libdriveio.so.0 /usr/lib/libdriveio.so.0
COPY --from=makemkv-builder /usr/lib/libmakemkv.so.1 /usr/lib/libmakemkv.so.1
COPY --from=makemkv-builder /usr/lib/libmmbd.so.0 /usr/lib/libmmbd.so.0
COPY --from=makemkv-builder /usr/share/MakeMKV /usr/share/MakeMKV

RUN ldconfig && ldd /usr/bin/makemkvcon

COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

EXPOSE 9160

VOLUME ["/config", "/output"]

ENTRYPOINT ["/app/entrypoint.sh"]
