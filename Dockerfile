FROM golang:1.26-alpine AS builder

WORKDIR /app

# Dependencies are vendored because the project uses a private/local Twill build.
# This keeps image builds reproducible without host paths or private Git credentials.
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -buildvcs=false -trimpath -ldflags="-s -w" \
    -o /predictmarket ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -buildvcs=false -trimpath -ldflags="-s -w" \
    -o /predictmarket-migrate ./cmd/migrate

# Final stage
FROM alpine:3.22

RUN apk --no-cache add ca-certificates tzdata

RUN addgroup -S predictmarket && adduser -S -G predictmarket predictmarket

WORKDIR /app

# Copy binary from builder
COPY --from=builder /predictmarket .
COPY --from=builder /predictmarket-migrate .

# Copy migrations
COPY --from=builder /app/migrations ./migrations

COPY --from=builder /app/twill.toml ./twill.toml

ENV SERVICETWILL_CONFIG=/app/twill.toml \
    XDG_DATA_HOME=/tmp/twill \
    APP_ENV=production \
    LOG_FORMAT=json \
    LOG_LEVEL=info

USER predictmarket

EXPOSE 8080

CMD ["./predictmarket"]
