FROM golang as build

WORKDIR /app
COPY utils/ ./utils/
COPY service_a/ ./service_a/
WORKDIR /app/service_a
RUN GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o service-a ./cmd/server

FROM scratch
WORKDIR /app
COPY --from=build /app/service_a/service-a .
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

ENTRYPOINT ["/app/service-a"]