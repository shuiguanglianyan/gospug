FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/gospug ./cmd/server

FROM alpine:3.20
WORKDIR /app
RUN apk add --no-cache sqlite
COPY --from=builder /bin/gospug /app/gospug
COPY web /app/web
EXPOSE 8080
ENTRYPOINT ["/app/gospug"]
