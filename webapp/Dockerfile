# tinku-webapp container image: a Vite build, then unprivileged nginx
# serving the static bundle and proxying the API's paths.
#
# Build from the REPOSITORY ROOT so version/ is in the build context
# alongside webapp/:
#
#   docker build -f webapp/Dockerfile -t tinku/webapp:dev .
FROM node:22-alpine AS builder
WORKDIR /build

COPY webapp/package.json webapp/package-lock.json* ./webapp/
RUN cd webapp && npm ci

COPY version/ ./version/
COPY webapp/ ./webapp/
RUN cd webapp && npm run build

# nginx-unprivileged is the same nginx and the same docker-entrypoint.d
# scripts as the official image — including the envsubst-on-templates step
# nginx.conf depends on — but already set up to run as an arbitrary non-root
# UID, so no chown or setcap dance is needed to satisfy a runAsNonRoot pod
# security context.
FROM nginxinc/nginx-unprivileged:1.27-alpine
COPY --from=builder /build/webapp/dist /usr/share/nginx/html
COPY webapp/nginx.conf /etc/nginx/templates/default.conf.template

# Turns on the base image's 15-local-resolvers.envsh, which reads the
# container's own nameservers into NGINX_LOCAL_RESOLVERS. nginx.conf's
# resolver directive uses it so proxying to the api defers DNS to request
# time instead of refusing to start when the api is not resolvable yet.
ENV NGINX_ENTRYPOINT_LOCAL_RESOLVERS=1

EXPOSE 8080
