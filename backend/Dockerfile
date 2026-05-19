FROM golang:1.24-alpine AS build

WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN go build -o /out/api ./cmd/api

FROM alpine:3.21

WORKDIR /app
RUN apk add --no-cache ca-certificates
COPY --from=build /out/api /app/api
EXPOSE 8088
CMD ["/app/api"]
