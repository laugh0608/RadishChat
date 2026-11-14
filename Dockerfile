FROM node:24-alpine AS web
WORKDIR /src
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/index.html web/tsconfig.json ./
COPY web/src ./src
RUN npm run build

FROM golang:1.26-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /radishchat ./cmd/radishchat

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && adduser -S -u 10001 radishchat && mkdir /data && chown 10001 /data
COPY --from=go /radishchat /usr/local/bin/radishchat
COPY --from=web /src/dist /app/web
USER 10001
ENV RADISHCHAT_LISTEN=0.0.0.0:8080 RADISHCHAT_DATABASE=/data/chat.sqlite RADISHCHAT_WEB_DIR=/app/web
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/radishchat"]
