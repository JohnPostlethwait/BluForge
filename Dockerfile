# Stage 1: Build Go app
FROM golang:1.25-alpine AS go-builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /build
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true
COPY . .
RUN CGO_ENABLED=0 go build -o bluforge .

# Stage 2: Build MakeMKV
FROM ubuntu:24.04 AS makemkv-builder

ARG MAKEMKV_VERSION=1.18.3
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential pkg-config wget ca-certificates \
    libc6-dev libssl-dev libexpat1-dev zlib1g-dev \
    libavcodec-dev libavutil-dev libavformat-dev \
    libgl1-mesa-dev qtbase5-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /tmp/makemkv

RUN wget -q "https://www.makemkv.com/download/makemkv-oss-${MAKEMKV_VERSION}.tar.gz" && \
    wget -q "https://www.makemkv.com/download/makemkv-bin-${MAKEMKV_VERSION}.tar.gz" && \
    tar xf "makemkv-oss-${MAKEMKV_VERSION}.tar.gz" && \
    tar xf "makemkv-bin-${MAKEMKV_VERSION}.tar.gz"

# Build and install the open-source library (libdriveio, libmakemkv)
RUN cd "makemkv-oss-${MAKEMKV_VERSION}" && \
    ./configure && make && make install

# Accept EULA and install makemkvcon binary
RUN mkdir -p "makemkv-bin-${MAKEMKV_VERSION}/tmp" && \
    echo "accepted" > "makemkv-bin-${MAKEMKV_VERSION}/tmp/eula_accepted" && \
    cd "makemkv-bin-${MAKEMKV_VERSION}" && \
    make install

# Stage 3: Runtime
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg ca-certificates \
    libssl3 libexpat1 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=go-builder /build/bluforge .
COPY --from=go-builder /build/static ./static
COPY --from=makemkv-builder /usr/bin/makemkvcon /usr/bin/makemkvcon
COPY --from=makemkv-builder /usr/lib/libdriveio.so.0 /usr/lib/libdriveio.so.0
COPY --from=makemkv-builder /usr/lib/libmakemkv.so.1 /usr/lib/libmakemkv.so.1
COPY --from=makemkv-builder /usr/share/MakeMKV /usr/share/MakeMKV

RUN ldconfig && makemkvcon --version

EXPOSE 9160

VOLUME ["/config", "/output"]

ENTRYPOINT ["/app/bluforge"]
