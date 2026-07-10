# Changelog

## v1.0.1 - 2026-07-10

- Correct Stash play-duration accounting by sending elapsed deltas instead of
  repeatedly adding the absolute playback position.
- Ignore forward seeks when calculating watched duration and suppress duplicate
  stopped events.
- Bound generated-cover image downloads and decoded dimensions to prevent memory
  spikes from oversized artwork.
- Support arbitrary Jellyfin `StartIndex` values, including paginated tag trees.
- Bound and validate JSON request bodies.
- Document the intentional password-only authentication model.
- Remove the unused stream-strategy setting and avoid UUID collisions for
  malformed entity identifiers.

## v1.0.0 - 2026-06-24

First public release.

- Jellyfin-compatible authentication and catalog browsing for Infuse.
- Scenes, performers, studios, and hierarchical tags from Stash.
- Scene metadata, cast, artwork, ratings, dates, runtime, and play state.
- Independent Date Added and Release Date sorting.
- Playback progress and completed-play writeback to Stash.
- Generated library covers with placeholder-image filtering and cache refresh.
- Direct playback redirects to Stash; media bytes do not pass through Stashfin.
