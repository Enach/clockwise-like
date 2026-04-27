FROM node:22-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install
COPY . .
RUN npm run build

FROM caddy:alpine
RUN apk upgrade --no-cache
COPY --from=builder /app/dist /srv
EXPOSE 80
CMD ["file-server", "--root", "/srv", "--listen", ":80"]
