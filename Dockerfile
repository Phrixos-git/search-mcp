FROM golang:1.27.1-alpine3.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/search-mcp ./cmd/search-mcp

FROM alpine:3.24.0
RUN addgroup -S -g 10001 searchmcp && adduser -S -D -H -u 10001 -G searchmcp searchmcp \
    && mkdir -p /data && chown searchmcp:searchmcp /data
COPY --from=build /out/search-mcp /usr/local/bin/search-mcp
USER 10001:10001
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/search-mcp"]
