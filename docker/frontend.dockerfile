# syntax=docker/dockerfile:1
# Build context: repo root (.)
FROM node:20-alpine AS node-builder
WORKDIR /app
COPY frontend/package.json frontend/package-lock.json ./
RUN npm install --legacy-peer-deps
COPY frontend/ .
RUN npm run build

FROM golang:1.25-alpine AS server-builder
WORKDIR /build
COPY docker/fileserver/ .
RUN CGO_ENABLED=0 GOOS=linux go build -o fileserver .

FROM scratch
COPY --from=server-builder /build/fileserver /fileserver
COPY --from=node-builder /app/dist /srv
EXPOSE 80
CMD ["/fileserver"]
