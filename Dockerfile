FROM node:24-bookworm-slim AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/konnyaku ./cmd/konnyaku

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --create-home konnyaku && mkdir /data && chown konnyaku:konnyaku /data
COPY --from=build /out/konnyaku /usr/local/bin/konnyaku
USER 10001:10001
WORKDIR /data
ENV LISTEN_ADDR=0.0.0.0:8080 REPOSITORY_ROOT=/data/repositories
EXPOSE 8080
ENTRYPOINT ["konnyaku"]
CMD ["serve"]
