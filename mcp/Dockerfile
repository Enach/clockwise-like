# syntax=docker/dockerfile:1
FROM golang:1.22-alpine AS builder
WORKDIR /app
RUN apk add --no-cache git
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GONOSUMDB='*' GOFLAGS='-mod=mod' go build -o mcp-server .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/mcp-server .
EXPOSE 3001
CMD ["./mcp-server"]
