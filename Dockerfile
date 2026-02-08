FROM golang:1.25-alpine

# Install git for go mod operations
RUN apk add --no-cache git

# Copy source and build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY src/ ./src/
RUN go build -o /usr/local/bin/go-safe-build ./src

# Copy entrypoint
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
