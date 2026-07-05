# smolanalytics — single static binary on distroless. No cgo, no cluster.
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/Arjun0606/smolanalytics/internal/api.Version=${VERSION}" \
    -o /smolanalytics ./cmd/smolanalytics

FROM gcr.io/distroless/static-debian12
LABEL io.modelcontextprotocol.server.name="io.github.Arjun0606/smolanalytics"
COPY --from=build /smolanalytics /smolanalytics
# containers must bind the wildcard for `-p` port mapping to work. `serve` on a public
# bind requires a password (set SMOLANALYTICS_PASSWORD); `demo` is exempt (throwaway data).
ENV ADDR=0.0.0.0:8080 SMOLANALYTICS_DB=/data/smolanalytics.data
VOLUME /data
EXPOSE 8080
# ENTRYPOINT is just the binary so `docker run <img> demo|serve|mcp` works;
# CMD defaults to serve.
ENTRYPOINT ["/smolanalytics"]
CMD ["serve"]
