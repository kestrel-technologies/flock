# flockd container image. Built by Flightpath on promotion to main; tag = VERSION.
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/flockd ./cmd/flockd

FROM alpine:3.20
RUN adduser -D -u 10001 flock
COPY --from=build /out/flockd /usr/local/bin/flockd
COPY config/ /etc/flock/
USER flock
ENTRYPOINT ["/usr/local/bin/flockd", "-tenants=/etc/flock/tenants.yaml", "-connectors=/etc/flock/connectors.yaml"]
