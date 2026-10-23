# syntax=docker/dockerfile:1.7
FROM golang:1.27.3-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5 AS build

WORKDIR /src
COPY tools/backupctl/go.mod tools/backupctl/go.sum ./
RUN go mod download
COPY tools/backupctl/ ./
# A static binary lets the runtime stage be empty apart from the tool itself.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/backupctl .

FROM scratch

# No shell, no package manager, no dynamic loader. TLS roots are copied because
# the tool must verify the endpoint certificate.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/backupctl /backupctl

USER 10001:10001
ENTRYPOINT ["/backupctl"]
