# Build the site, then serve it. Two stages so the runtime image carries a
# web server and static files, and nothing that produced them.
FROM python:slim AS builder

# git: the build installs PySocha from a pinned commit, so the pin
# lives in exactly one place, and uv fetches PySocha from git.
RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates \
    && rm -rf /var/lib/apt/lists/*
RUN pip install --no-cache-dir uv

WORKDIR /build
COPY . .

# Writes to ./site, per site-src/config.yaml.
RUN ./tools.sh build

FROM caddy:2.9.1-alpine

WORKDIR /site
RUN mkdir /site/logs

COPY Caddyfile /etc/caddy/Caddyfile
COPY --from=builder /build/site ./root
