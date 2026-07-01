FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS go-builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH

RUN GOOS=$TARGETOS \
    GOARCH=$TARGETARCH \
    go build \
    -o sal .


FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    libssl3 \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=go-builder /app/sal /app/sal

ENTRYPOINT [ "/app/sal" ]