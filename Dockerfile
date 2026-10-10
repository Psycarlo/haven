# syntax=docker/dockerfile:1

# Both database engines need cgo, so the relay is built with a C toolchain and
# runs on the same libc.
FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags "-s -w" -o /out/haven .

FROM debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/* \
  && useradd --system --uid 1000 --user-group --home-dir /app --no-create-home haven
WORKDIR /app
COPY --from=build /out/haven ./haven
COPY templates ./templates
COPY relays_import.example.json ./relays_import.json
COPY relays_blastr.example.json ./relays_blastr.json
COPY --chmod=0755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
ENV IMPORT_SEED_RELAYS_FILE=relays_import.json \
  BLASTR_RELAYS_FILE=relays_blastr.json
VOLUME ["/app/db", "/app/blossom"]
EXPOSE 3355
ENTRYPOINT ["docker-entrypoint.sh", "./haven"]
