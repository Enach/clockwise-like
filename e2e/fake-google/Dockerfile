# The e2e stand-in for the Google Calendar API, and the recording sink.
# Two binaries in one image; the compose file picks one with `command:`.
# Mirrors backend/Dockerfile: build on golang:alpine, run FROM scratch.
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/fake-google . \
 && CGO_ENABLED=0 GOOS=linux go build -o /out/sink ./sink

FROM scratch
COPY --from=builder /out/fake-google /fake-google
COPY --from=builder /out/sink /sink
EXPOSE 8080
CMD ["/fake-google"]
