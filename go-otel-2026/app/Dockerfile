FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/order-api .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates
COPY --from=build /out/order-api /usr/local/bin/order-api

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/order-api"]
