# Build Stage
FROM golang:alpine AS builder

# Install git. Git is required for fetching the dependencies.
RUN apk update && apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the Go app (statically linked for Alpine)
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main .

# ==========================================
# Run Stage
FROM scratch

# Copy CA certificates to allow HTTPS calls
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

WORKDIR /root/

# Copy the pre-built binary file from the previous stage
COPY --from=builder /app/main .

# Expose port 3000 to the outside world (within k8s cluster)
EXPOSE 3000

# Command to run the executable
CMD ["./main"]
