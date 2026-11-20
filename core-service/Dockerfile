# syntax=docker/dockerfile:1
# ---- tahap 1: build binary (image besar, berisi compiler Go) ----
FROM golang:1.26-alpine AS build
WORKDIR /src

# Salin go.mod/go.sum dulu: layer download dependency di-cache selama dependency tidak berubah
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO_ENABLED=0 → binary statis, bisa jalan di image tanpa libc
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app .

# ---- tahap 2: image akhir (kecil, tanpa shell dan compiler, user non-root) ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
EXPOSE 8081
USER nonroot:nonroot

# Exec form (tanpa shell, karena distroless tidak punya sh/curl): binary mengecek dirinya sendiri.
# start-period: waktu untuk konek DB + RabbitMQ + migrasi sebelum kegagalan mulai dihitung.
HEALTHCHECK --interval=10s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/app", "-healthcheck"]

ENTRYPOINT ["/app"]
