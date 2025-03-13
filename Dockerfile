# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git

# Set working directory
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o goardian ./cmd/goardian

# Final stage
FROM scratch

# Copy binary from builder
COPY --from=builder /app/goardian /goardian

# Expose metrics port
EXPOSE 9090

# Run the application
CMD ["/goardian"] 