FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /relay ./cmd/relay/

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /relay /app/relay
WORKDIR /app
EXPOSE 8080
ENTRYPOINT ["/app/relay"]
CMD ["-config", "config.yaml"]
