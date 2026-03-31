FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /sample-app .

FROM alpine:latest
RUN addgroup -S app && adduser -S app -G app
COPY --from=builder /sample-app /bin/sample-app
EXPOSE 8080
USER app
ENTRYPOINT ["/bin/sample-app"]
