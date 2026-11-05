# Changelog

## [0.2.1] — 2026-11-05

First public release.

- Module path `github.com/osipovgoga33/sap-help-mcp`; own User-Agent
  `sap-help-mcp-go/<version>`.
- Releases are built by GitHub Actions from `main` when the version changes.
- The `.mcpb` ships without an icon.

## [0.2.0] — 2026-11-05

- SAP Community behind a Cloudflare check: `community_search` and
  `community_read` return `status: browser_required` and a script that runs the
  same queries, ranking and formatting in the built-in browser of Claude
  Desktop. The user ticks the check if one appears; Claude never does.
- After a Cloudflare check the forum is not called directly for 30 minutes.

## [0.1.2] — 2026-11-05

- Fix: HTTP/1.1 was not actually used — the cloned Go transport still offered
  `h2` via ALPN, the forum chose HTTP/2 and the client failed with
  "malformed HTTP response". ALPN is now `http/1.1` only.

## [0.1.1] — 2026-11-05

- Requests use HTTP/1.1.
- A 403/429 from a portal includes the response headers and the start of the
  page, which show who is blocking.

## [0.1.0] — 2026-11-04

- First version: one tool `sap_help` with `search`, `read`,
  `community_search`, `community_read`, `status`.
