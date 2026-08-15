# Known Limitations

Source: SPEC.md §10.

- **Stories**: Instagram serves stories only to authenticated sessions in virtually all cases; without IG login this will fail for real-world stories despite being nominally "in scope." Bot returns a clear "couldn't fetch, may require login" error rather than a generic failure.
- **IG anti-scraping changes**: yt-dlp's IG extractor breaks periodically when Instagram changes its API. Confirmed during spec validation: the `choco`-packaged yt-dlp (2025.07.21) could not be assumed current — always run a self-updating install (pip, or the standalone binary with the update timer, see [deployment.md](deployment.md)) rather than a distro/package-manager version that goes stale.
- **Age-restricted / login-required posts**: fail cleanly with an explanatory reply.
- **Cache key is the raw URL string**: two links that point at the same media but differ in query string (e.g. `?igsh=...` tracking params) are cached separately. Acceptable for v1; normalizing the URL (strip known tracking params) would be a small follow-up if cache-miss rate on effectively-duplicate links turns out to matter.
- Single VPS, single bot instance — no HA, acceptable for personal use.
