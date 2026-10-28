FROM golang:1.25-alpine AS builder
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o server . \
 && CGO_ENABLED=0 GOOS=linux go build -o paceday-operator ./cmd/paceday-operator

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /app/server /server
# PAC-71 operator procedure (docs/operator/PAC-71-account-recovery.md):
# docker compose -f docker-compose.prod.yml exec backend /paceday-operator ...
COPY --from=builder /app/paceday-operator /paceday-operator
EXPOSE 8080
CMD ["/server"]
