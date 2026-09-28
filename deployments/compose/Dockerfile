FROM node:22-bookworm-slim AS webbuild
WORKDIR /src/apps/web
COPY apps/web/package.json apps/web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY apps/web ./
RUN npm run build

FROM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/control-plane ./cmd/control-plane \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/admin-bootstrap ./cmd/admin-bootstrap \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/node-bootstrap ./cmd/node-bootstrap \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/agent-token ./cmd/agent-token

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
COPY --from=build /out/control-plane /usr/local/bin/control-plane
COPY --from=build /out/migrate /usr/local/bin/migrate
COPY --from=build /out/admin-bootstrap /usr/local/bin/admin-bootstrap
COPY --from=build /out/node-bootstrap /usr/local/bin/node-bootstrap
COPY --from=build /out/agent-token /usr/local/bin/agent-token
COPY migrations /app/migrations
COPY --from=webbuild /src/apps/web/dist /app/web
USER nonroot:nonroot
EXPOSE 8080
CMD ["/usr/local/bin/control-plane"]
