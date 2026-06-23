FROM golang:1.24-alpine AS build

ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X github.com/BaksiLi/stashfin/internal/buildinfo.Version=${VERSION} -X github.com/BaksiLi/stashfin/internal/buildinfo.Commit=${COMMIT}" \
    -o /out/stashfin ./cmd/stashfin

FROM alpine:3.22

RUN addgroup -S stashfin && adduser -S -G stashfin stashfin
USER stashfin

COPY --from=build /out/stashfin /usr/local/bin/stashfin

EXPOSE 8096
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD wget -qO- http://127.0.0.1:8096/healthz >/dev/null || exit 1
ENTRYPOINT ["stashfin"]
