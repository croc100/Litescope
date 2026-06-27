# Builds the LiteScope MCP server image used by Glama's introspection checks.
# The container starts the stdio MCP server (`litescope mcp`), which responds to
# initialize / tools/list over stdin/stdout.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Pure-Go SQLite driver (modernc.org/sqlite) — no cgo needed.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /litescope ./cmd/litescope

FROM alpine:3.21
RUN adduser -D -u 10001 app
USER app
COPY --from=build /litescope /usr/local/bin/litescope
# Read-only stdio MCP server. Pass --allow-writes to enable write tools.
ENTRYPOINT ["litescope", "mcp"]
