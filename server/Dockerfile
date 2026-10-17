# The API image. Only the production binary is built: it imports nothing but
# the standard library and this module, so the build needs no downloads.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /api .

# distroless/static: no shell, a nonroot user, and the static binary.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
