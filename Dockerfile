# Qwen-Free-API — production-ish single-stage build
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /qwen-api .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 bridge
WORKDIR /app
COPY --from=builder /qwen-api /app/qwen-api
COPY .env.example /app/.env.example
USER bridge
ENV PORT=8080 HOST=0.0.0.0
EXPOSE 8080
ENTRYPOINT ["/app/qwen-api"]
