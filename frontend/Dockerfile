# syntax=docker/dockerfile:1
FROM node:22-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci

FROM node:22-alpine AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
# Rewrites are resolved at build time; in Compose the API is reachable as "backend".
ARG API_INTERNAL_URL=http://backend:8080
# Demo mode (true): the UI runs entirely on an in-browser mock backend. Inlined at build time.
ARG NEXT_PUBLIC_MOCK_MODE=false
ENV API_INTERNAL_URL=$API_INTERNAL_URL NEXT_PUBLIC_MOCK_MODE=$NEXT_PUBLIC_MOCK_MODE NEXT_TELEMETRY_DISABLED=1
RUN npm run build

FROM node:22-alpine
WORKDIR /app
ENV NODE_ENV=production NEXT_TELEMETRY_DISABLED=1 PORT=3000 HOSTNAME=0.0.0.0
RUN addgroup -S app && adduser -S -G app app
COPY --from=build --chown=app:app /app/.next/standalone ./
COPY --from=build --chown=app:app /app/.next/static ./.next/static
COPY --from=build --chown=app:app /app/public ./public
USER app
EXPOSE 3000
CMD ["node", "server.js"]
