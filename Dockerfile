# syntax=docker/dockerfile:1

FROM node:22-alpine AS web
WORKDIR /ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

FROM golang:1.24-alpine AS build

ARG CURL_VERSION=8.21.0
ARG CURL_SHA256=d9b327997999045a24cda50f3983e69e51c516bd8be6ef9842fc7f99135e33bb

RUN apk add --no-cache curl gcc musl-dev make perl mbedtls-dev mbedtls-static zlib-dev zlib-static ca-certificates \
 && curl -fsSL "https://curl.se/download/curl-${CURL_VERSION}.tar.gz" -o /curl.tar.gz \
 && echo "${CURL_SHA256}  /curl.tar.gz" | sha256sum -c - \
 && mkdir /src \
 && tar xzf /curl.tar.gz -C /src --strip-components=1 \
 && cd /src \
 && ./configure --prefix=/out \
      --disable-shared --enable-static \
      --with-mbedtls --with-zlib \
      --disable-ldap --disable-ldaps \
      --without-libidn2 --without-librtmp --without-libpsl \
      --without-nghttp2 --without-brotli \
      --disable-docs --disable-manual \
 && make -j"$(nproc)" LDFLAGS="-all-static" \
 && make install \
 && cd / \
 && rm -rf /src /curl.tar.gz

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /internal/spa/dist ./internal/spa/dist
RUN CGO_ENABLED=0 go build -o /cronix ./cmd/cronix

FROM scratch

COPY --from=build /out/bin/curl /usr/local/bin/curl
COPY --from=build /cronix /cronix
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

ENV CURL_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
ENTRYPOINT ["/cronix"]