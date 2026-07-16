# Build the mesh relay. Self-contained module; pure Go (libp2p + quic-go),
# so CGO off → a static binary that runs on distroless/static.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOFLAGS=-trimpath go build -ldflags="-s -w" -o /relay .

# Minimal, non-root runtime.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /relay /relay
# Circuit Relay v2 rides QUIC over UDP. Publish with `-p 4001:4001/udp`.
EXPOSE 4001/udp
ENTRYPOINT ["/relay"]
