# The API image. Only the production binary is built: it imports the
# standard library, this module and golang.org/x/crypto/bcrypt (passwords),
# so that one module is all the build downloads; its own layer keeps it
# cached while the code changes.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download golang.org/x/crypto
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /api .

# distroless/static: no shell, a nonroot user, and the static binary.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
