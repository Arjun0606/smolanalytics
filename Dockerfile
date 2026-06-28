# smolanalytics — single static binary on distroless. No cgo, no cluster.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/Arjun0606/smolanalytics/internal/api.Version=${VERSION}" \
    -o /smolanalytics ./cmd/smolanalytics

FROM gcr.io/distroless/static-debian12
COPY --from=build /smolanalytics /smolanalytics
ENV ADDR=:8080 SMOLANALYTICS_DB=/data/smolanalytics.data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/smolanalytics", "serve"]
