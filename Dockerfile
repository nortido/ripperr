FROM golang:1.20 as builder
COPY . /app
ENV GOOS=linux CGO_ENABLED=0
WORKDIR /app
RUN go build && ls -la

FROM alpine:3.16
COPY --from=builder /app/torr /app/
WORKDIR /app
CMD /app/torr
EXPOSE 6410