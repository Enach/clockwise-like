# syntax=docker/dockerfile:1
# Build context: a checkout of Enach/smart-calendar-flow (see ADR-0002).
# CI passes ./frontend (cloned at run-time). Local dev passes ../smart-calendar-flow.
FROM node:20-alpine AS node-builder
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm install --legacy-peer-deps
COPY . .
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
