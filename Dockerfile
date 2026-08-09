FROM docker.io/library/golang:1.25-bookworm AS builder

RUN apt-get update \
    && apt-get install -y --no-install-recommends gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o /frg-node ./cmd/frg-node

FROM docker.io/library/debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates gosu tzdata \
    && rm -rf /var/lib/apt/lists/*
RUN groupadd --system frg && useradd --system --gid frg --home-dir /var/lib/frg frg \
    && mkdir -p /var/lib/frg \
    && chown -R frg:frg /var/lib/frg
COPY --from=builder /frg-node /usr/local/bin/frg-node
COPY docker/frg-node-entrypoint.sh /usr/local/bin/frg-node-entrypoint
RUN chmod +x /usr/local/bin/frg-node-entrypoint
WORKDIR /var/lib/frg
EXPOSE 7777 50051 9090
ENTRYPOINT ["frg-node-entrypoint"]
