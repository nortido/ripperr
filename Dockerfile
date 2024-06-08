FROM golang:1.20 as builder
RUN go install github.com/go-delve/delve/cmd/dlv@latest
ENV GOOS=linux CGO_ENABLED=0
COPY . /app
WORKDIR /app
RUN go build ./cmd/*.go && ls -la

FROM alpine:3.16
RUN apk add gcompat
WORKDIR /app
COPY --from=builder /go/bin/dlv /dlv
COPY --from=builder /app/main /app/
EXPOSE 6410 2345

#CMD ["/dlv", "--listen=:2345", "--headless=true", "--api-version=2", "--accept-multiclient", "exec", "/app/main"]
CMD /app/main