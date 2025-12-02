FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod .
COPY main.go .
RUN CGO_ENABLED=0 go build -o /out/local-proxy .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/local-proxy /usr/local/bin/local-proxy

EXPOSE 80 2080
ENTRYPOINT ["/usr/local/bin/local-proxy"]
