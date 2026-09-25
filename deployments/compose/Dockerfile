FROM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/control-plane ./cmd/control-plane \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/admin-bootstrap ./cmd/admin-bootstrap

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
COPY --from=build /out/control-plane /usr/local/bin/control-plane
COPY --from=build /out/migrate /usr/local/bin/migrate
COPY --from=build /out/admin-bootstrap /usr/local/bin/admin-bootstrap
COPY migrations /app/migrations
USER nonroot:nonroot
EXPOSE 8080
CMD ["/usr/local/bin/control-plane"]
