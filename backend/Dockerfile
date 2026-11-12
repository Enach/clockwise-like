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
# Operator command. PAC-71 account steps (docs/operator/PAC-71-account-recovery.md)
# run against the running backend:
#   docker compose -f docker-compose.prod.yml exec backend /paceday-operator <command> ...
# PAC-84 encryption subcommands that need the backend stopped (decrypt-all,
# reset-lost-key, downgrade-schema; docs/runbook.md "Encryption key"):
#   docker compose -f docker-compose.prod.yml run --rm --no-deps backend /paceday-operator encryption <subcommand> ...
COPY --from=builder /app/paceday-operator /paceday-operator
EXPOSE 8080
CMD ["/server"]
