FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o /frg-node ./cmd/frg-node

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /frg-node /usr/local/bin/frg-node
ENTRYPOINT ["frg-node"]
