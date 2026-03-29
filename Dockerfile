# Stage 1: Build
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /build
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true
COPY . .
RUN CGO_ENABLED=0 go build -o bluforge .

# Stage 2: Runtime
FROM alpine:3.20

RUN apk add --no-cache ffmpeg ca-certificates

# Note: MakeMKV (makemkvcon) must be available in the runtime image.
# For production, use a base image with MakeMKV pre-installed (e.g., jlesage/makemkv)
# or build MakeMKV from source in an additional build stage.

WORKDIR /app
COPY --from=builder /build/bluforge .
COPY --from=builder /build/static ./static

EXPOSE 9160

VOLUME ["/config", "/output"]

ENTRYPOINT ["/app/bluforge"]
