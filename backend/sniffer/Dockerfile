# Bullseye Sniffer — multi-stage build
# Build context: backend/sniffer/
FROM golang:1.23-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /sniffer -ldflags="-s -w" ./cmd/sniffer

# runtime — needs libpcap for AF_PACKET
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata libpcap
COPY --from=builder /sniffer /sniffer

ENTRYPOINT ["/sniffer"]
