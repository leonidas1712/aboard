# The aboard team server: the binary with the board view, run as a non-root user with its
# data in /data/aboard on a volume at /data. Build it from the repository root:
#
#   docker build --build-arg VERSION=0.1.0 -t aboard:0.1.0 .
#
# and run it behind a proxy that ends HTTPS:
#
#   docker run -v aboard-data:/data -p 127.0.0.1:7400:7400 \
#     -e ABOARD_PUBLIC_URL=https://aboard.example.com aboard:0.1.0
#
# docs/team-server.mdx has the rest, a Kubernetes recipe included.

# The web UI: a static Next.js export in web/out.
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN NEXT_TELEMETRY_DISABLED=1 npm run build

# The binary, static, with the UI embedded by the ui tag. VERSION stamps the release
# version, as a release build does; without it the source's version is used.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# Only the packages the binary is built from (go list -deps ./server/cmd/aboard).
COPY adapters/ adapters/
COPY server/ server/
COPY skills/ skills/
COPY spec/ spec/
COPY web/*.go web/
COPY --from=web /src/web/out ./web/out
ARG VERSION=""
RUN CGO_ENABLED=0 go build -trimpath -tags ui \
      -ldflags "-s -w ${VERSION:+-X github.com/leonidas1712/aboard/server/internal/cli.version=${VERSION}}" \
      -o /out/aboard ./server/cmd/aboard

# A small image with a shell, so an operator can read the first admin's key file with
# kubectl exec or docker exec.
FROM alpine:3.22
RUN addgroup -S -g 10001 aboard && adduser -S -D -H -u 10001 -G aboard -h /data aboard \
 && mkdir -p /data && chown aboard:aboard /data && chmod 700 /data
COPY --from=build /out/aboard /usr/local/bin/aboard
USER 10001:10001
ENV ABOARD_DATA=/data/aboard ABOARD_LISTEN=0.0.0.0:7400 ABOARD_NO_UPDATE_CHECK=1
VOLUME /data
EXPOSE 7400
ENTRYPOINT ["aboard", "serve", "--team"]
