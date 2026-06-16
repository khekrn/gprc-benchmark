# --- Build stage --------------------------------------------------------
FROM golang:1.25 AS build
WORKDIR /src

# Copy modules first so dependency download caches.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/users-go .

# --- Runtime stage ------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/khekrn/gprc-benchmark"

COPY --from=build /out/users-go /users-go
COPY --from=build /src/config.yaml /config.yaml

EXPOSE 8080 9090
USER nonroot:nonroot
ENTRYPOINT ["/users-go"]
