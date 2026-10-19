# ---- build stage ----
FROM golang:1.24-alpine AS build
WORKDIR /src

# Download deps first so this layer is cached when only code changes.
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
# Static linux/amd64 binary (Fargate runs x86_64 by default).
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o product-service .

# ---- runtime stage ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /src/product-service .
USER app

# Must match container_port in terraform/variables.tf.
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["./product-service"]
