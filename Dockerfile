FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o /frg-node ./cmd/frg-node

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
RUN addgroup -S frg && adduser -S -G frg frg \
    && mkdir -p /var/lib/frg \
    && chown -R frg:frg /var/lib/frg
COPY --from=builder /frg-node /usr/local/bin/frg-node
COPY docker/frg-node-entrypoint.sh /usr/local/bin/frg-node-entrypoint
RUN chmod +x /usr/local/bin/frg-node-entrypoint
WORKDIR /var/lib/frg
USER frg
EXPOSE 7777 50051 9090
ENTRYPOINT ["frg-node-entrypoint"]
