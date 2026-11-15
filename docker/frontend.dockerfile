# syntax=docker/dockerfile:1
# Build context: this repo root (.). The frontend/ directory is the
# smart-calendar-flow clone (CI clones it at run-time; local dev runs
# `make clone-frontend`). docker/fileserver is the static-file server
# we ship with the SPA bundle.
FROM node:20-alpine AS node-builder
WORKDIR /app
COPY frontend/package.json frontend/package-lock.json ./
RUN npm install --legacy-peer-deps
COPY frontend/ .
# PAC-121: the web app's Sentry DSN is baked into the bundle at build time
# (Vite inlines VITE_* variables). The web has no built-in DSN (PAC-86), so
# empty, the default, means web monitoring is off. The release workflow
# passes it from the repository *variable* VITE_SENTRY_DSN (not a secret:
# a frontend DSN ships in the public bundle). When it is set, the deployment's
# NGINX_CSP_SENTRY_ORIGIN must be its scheme://host origin.
ARG VITE_SENTRY_DSN=""
ENV VITE_SENTRY_DSN=${VITE_SENTRY_DSN}
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
