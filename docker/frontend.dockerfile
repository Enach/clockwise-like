# syntax=docker/dockerfile:1
FROM node:20-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm install --legacy-peer-deps
COPY . .
RUN npm run build

FROM caddy:2-alpine
RUN apk upgrade --no-cache
COPY --from=builder /app/dist /srv
EXPOSE 80
CMD ["file-server", "--root", "/srv", "--listen", ":80"]
