FROM golang:1.25.12-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY api ./api
COPY cmd ./cmd
COPY internal ./internal
COPY paykit ./paykit
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/commerce ./cmd/commerce

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/commerce /usr/local/bin/commerce
COPY --chown=nonroot:nonroot manifest ./manifest

ENV GF_SERVER_ADDRESS=0.0.0.0:8084
ENV OTEL_TRACES_EXPORTER=none

EXPOSE 8084
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/commerce"]
