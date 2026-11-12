# syntax=docker/dockerfile:1

# 1. Build the web console.
FROM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json web/.npmrc ./
RUN npm ci --no-audit --no-fund --ignore-scripts
COPY web/ ./
RUN npm run build

# 2. Build the server with the console embedded.
FROM golang:1.27-alpine AS server
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
COPY --from=web /web/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /slicewise ./cmd/slicewise

# 3. A minimal runtime image: no shell, no package manager, non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /slicewise /slicewise
EXPOSE 8080
ENTRYPOINT ["/slicewise", "-addr", ":8080"]
