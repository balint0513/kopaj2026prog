# Stage 1: Build static Go binary
FROM golang:alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/lang .

# Stage 2: Minimal distroless runtime (~2 MB base)
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/lang /lang
ENTRYPOINT ["/lang"]
