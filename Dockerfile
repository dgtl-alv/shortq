FROM golang:1.23-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum* ./
RUN go mod download
COPY cmd cmd
COPY internal internal
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/shortq ./cmd/server

FROM alpine:3.20
WORKDIR /app
RUN apk add --no-cache ca-certificates && addgroup -S shortq && adduser -S -G shortq shortq
COPY --from=build /out/shortq /app/shortq
COPY web /app/web
COPY docs /app/docs
RUN chmod -R a+rX /app/web /app/docs
EXPOSE 8080
USER shortq
CMD ["/app/shortq"]
