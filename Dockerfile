# syntax=docker/dockerfile:1

FROM golang:1.27.3-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY pkg ./pkg

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/http-server \
    ./cmd/http_server

FROM alpine:3.24

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app app

COPY --from=build /out/http-server /usr/local/bin/http-server

USER 10001:10001
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/http-server"]
