# Stashfin

[![CI](https://github.com/BaksiLi/stashfin/actions/workflows/ci.yml/badge.svg)](https://github.com/BaksiLi/stashfin/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/BaksiLi/stashfin)](https://github.com/BaksiLi/stashfin/releases)
[![License](https://img.shields.io/github/license/BaksiLi/stashfin)](LICENSE)

Stashfin is a focused Jellyfin-compatible bridge from
[Stash](https://github.com/stashapp/stash) to clients such as Infuse. It exposes
a browsable media catalog while keeping video bytes out of the bridge.

<p align="center">
  <img src="docs/assets/cover-preview.svg" width="920" alt="Stashfin generated library cover system">
</p>

## How It Works

**Control plane:** Infuse talks to Stashfin using the Jellyfin API. Stashfin
reads metadata from Stash over GraphQL and writes back playback activity,
resume position, and ratings.

**Media plane:** Stashfin returns a `302` redirect to a Stash-generated stream
URL. Infuse then streams directly from Stash using its normal HTTP Range
requests. Video chunks never pass through the Go service.

This split is the main design constraint: Stashfin translates protocol and
metadata, but it is not a second media server.

## Features

- Four libraries: `Scenes`, `Performers`, `Studios`, and hierarchical `Tags`.
- Scene metadata including artwork, overview, cast, studio, tags, rating,
  runtime, Date Added, Release Date, and play state.
- Performer, studio, and tag folders that open directly to matching scenes.
- Separate performer identities for browsable folders and Jellyfin cast people.
- Sorting by name, added date, release date, runtime, play count, play date,
  and rating where Stash supports the corresponding sort.
- Generated library covers built from recent Stash artwork, with placeholder
  filtering and image-tag cache busting.
- Playback progress, resume position, completed plays, and numeric ratings
  written back to Stash.
- Direct stream redirects with no video proxying or transcoding in Stashfin.

## Quick Start

```bash
git clone https://github.com/BaksiLi/stashfin.git
cd stashfin
cp .env.example .env
```

Set at least:

```dotenv
STASHFIN_SERVER_ID=a-stable-random-id
STASHFIN_USER=stashfin
STASHFIN_PASSWORD=change-me
STASH_INTERNAL_URL=http://stash:9999
STASH_PUBLIC_URL=http://your-stash-host:9999
STASH_API_KEY=your-stash-api-key
```

Then start the service:

```bash
docker compose up -d --build
```

Add `http://your-stashfin-host:19998` as a Jellyfin share in Infuse. The
container listens on `8096`; Compose publishes it on `19998` by default.

Tagged releases also publish Linux binaries and multi-architecture container
images at `ghcr.io/baksili/stashfin`.

## URL Configuration

- `STASH_INTERNAL_URL` is the URL Stashfin uses to reach Stash, commonly
  `http://stash:9999` on a shared Docker network.
- `STASH_PUBLIC_URL` is the URL the playback client can reach over LAN or VPN.

Stashfin rewrites the internal origin in Stash-generated stream URLs to the
public origin before redirecting the client.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `STASHFIN_PUBLISHED_PORT` | `19998` | Host port used by Compose |
| `STASHFIN_SERVER_NAME` | `Stashfin` | Name shown to Jellyfin clients |
| `STASHFIN_SERVER_ID` | required | Stable server identity |
| `STASHFIN_USER` | `stashfin` | Jellyfin login name |
| `STASHFIN_PASSWORD` | required | Jellyfin login password |
| `STASH_INTERNAL_URL` | `http://stash:9999` | Stash URL reachable from Stashfin |
| `STASH_PUBLIC_URL` | required | Stash URL reachable from the client |
| `STASH_API_KEY` | required | Stash GraphQL and stream credential |
| `STASH_TIMEOUT` | `15s` | Stash request timeout |

See [.env.example](.env.example) and [compose.yaml](compose.yaml) for the full
deployment shape, including optional Traefik labels.

## Compatibility

The v1 release is exercised against:

- Stash `v0.30.1`
- Infuse `8.4.4` on iPadOS

Stashfin reports a Jellyfin-compatible server version to clients. Its own build
version is available in the `X-Stashfin-Version` response header and
`GET /healthz`.

Stashfin intentionally implements the Jellyfin surface needed by this workflow,
not the complete Jellyfin API. Unknown endpoints return conservative empty
responses where that keeps clients functional.

## Security

This project is designed for a trusted LAN or VPN. Stash stream URLs may contain
the Stash API key, so do not expose the direct Stash media endpoint to the public
internet without a purpose-built boundary that keeps the credential server-side.

## Scope

Stashfin deliberately does not include transcoding, stream byte forwarding, a
configuration UI, client-profile matrices, playlist editing, or series
emulation. Those features belong only if a concrete client workflow requires
them without compromising the direct-media architecture.

The project draws useful compatibility lessons from
[feldorn/Stash-Jellyfin-Proxy](https://github.com/feldorn/Stash-Jellyfin-Proxy),
especially around hierarchy, image caching, and Jellyfin client behavior.

## Development

```bash
go test ./...
go vet ./...
go run ./cmd/stashfin
```

Release history is recorded in [CHANGELOG.md](CHANGELOG.md).
