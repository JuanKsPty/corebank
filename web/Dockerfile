# =============================================================================
# corebank web
#
# Alpine is fine here, unlike in the API image: the frontend has no native
# dependencies, so nothing needs glibc.
#
# The build takes no VITE_* variables. That is deliberate — Vite inlines them at
# compile time, so an API URL baked into the bundle would be wrong the moment the
# stack ran anywhere other than the machine that built it. nginx proxies /api to
# the backend instead, and the browser only ever sees one origin.
# =============================================================================

# --- build -------------------------------------------------------------------
FROM node:22-alpine AS build

WORKDIR /src

# `npm ci` rather than `npm install`: it installs exactly what the lockfile says
# and fails if the two disagree, which is what a reproducible image needs.
COPY package.json package-lock.json ./
RUN npm ci

COPY . .

# `npm run build` is `tsc -b && vite build`, so a type error fails the image
# rather than shipping.
RUN npm run build

# --- runtime -----------------------------------------------------------------
FROM nginx:1.29-alpine

# The stock config listens on 80 and would need root to bind it. Replacing it
# with one on 8080 is what lets this run unprivileged.
RUN rm /etc/nginx/conf.d/default.conf
COPY nginx.conf /etc/nginx/conf.d/corebank.conf

COPY --from=build /src/dist /usr/share/nginx/html

# nginx:alpine already defines the `nginx` user (uid 101). It needs to own the
# directories it writes at runtime — the pid file and its temp paths — because
# nothing else here should be writable.
RUN touch /var/run/nginx.pid \
 && chown -R nginx:nginx /var/run/nginx.pid /var/cache/nginx /usr/share/nginx/html

USER nginx

EXPOSE 8080

# `daemon off` keeps nginx in the foreground, which is what a container's process
# 1 has to be for Docker to see it stop.
CMD ["nginx", "-g", "daemon off;"]
