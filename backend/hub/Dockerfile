# Bullseye Hub — multi-stage build
# Build context: backend/hub/
FROM golang:1.23-alpine AS builder

WORKDIR /src

# cache deps
COPY go.mod go.sum ./
RUN go mod download

# copy source + build
COPY . .
RUN CGO_ENABLED=0 go build -o /hub -ldflags="-s -w" ./cmd/hub

# runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /hub /hub

ENTRYPOINT ["/hub"]
