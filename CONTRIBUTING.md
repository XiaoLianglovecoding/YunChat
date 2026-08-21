# Contributing

## Branch and commit convention

- Branch: `feat/<scope>`, `fix/<scope>`, `docs/<scope>`
- Commit: Conventional Commits, for example `feat(message): persist text messages`
- One pull request should implement one coherent use case.

## Definition of done

1. Replace the related `TODO(linknest)` marker with an implementation.
2. Add unit tests for application rules and integration tests for adapters.
3. Update OpenAPI and WebSocket protocol documents when contracts change.
4. Run `scripts/check.ps1` before opening a pull request.
5. Do not commit local secrets, uploads, generated bundles, or database data.

## Architecture boundary

`domain` must not import transport or infrastructure packages. `application`
depends on domain ports; adapters implement those ports. Cross-feature calls go
through application interfaces instead of reaching into another repository.

