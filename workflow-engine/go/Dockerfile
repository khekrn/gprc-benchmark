# Build stage
FROM golang:1.21-alpine AS builder

# Install required packages
RUN apk add --no-cache git protobuf-dev

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN go build -o workflow-engine ./cmd/server

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from builder stage
COPY --from=builder /app/workflow-engine .

# Copy the .env file
COPY --from=builder /app/.env .

# Expose ports
EXPOSE 8080 9090

# Run the application
CMD ["./workflow-engine"]