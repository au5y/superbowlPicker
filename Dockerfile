# Build Stage
FROM golang:1.23-alpine AS builder

# Install C compiler required for go-sqlite3
RUN apk add --no-cache gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=1 is required for sqlite3
RUN CGO_ENABLED=1 GOOS=linux go build -o superbowl-picker .

# Run Stage
FROM alpine:latest

WORKDIR /root/

# Copy the binary from builder
COPY --from=builder /app/superbowl-picker .

# Copy static assets and templates (crucial for your app structure)
COPY --from=builder /app/static ./static
COPY --from=builder /app/templates ./templates

# Create a directory for the database to mount a volume to
RUN mkdir -p /data

# Set env vars to point DB to the volume
ENV DB_NAME=/data/game.db
ENV PORT=4884

EXPOSE 4884

CMD ["./superbowl-picker"]