# sap-help-mcp (Go)

An MCP server for Claude that searches and reads **help.sap.com** (official
documentation plus the Support Content space) and **SAP Community** (blogs,
Q&A, discussions) live, so answers come from the page instead of memory.

A single self-contained Go binary — no Python, no Node, no runtime downloads.

[Русская версия](README.ru.md)

- No credentials, nothing written to disk: only anonymous GET requests to public
  portal endpoints.
- Responses are cached in memory for 6 hours; 200 ms pause between requests.
- One tool, `sap_help`, with an `action` parameter — Claude Desktop asks for
  permission once instead of five times.

## Download

Latest release: <https://github.com/osipovgoga33/sap-help-mcp/releases/latest>

| File | Platform |
|---|---|
| `sap-help.exe` | Windows x64 |
| `sap-help-darwin-arm64` | macOS Apple Silicon |
| `sap-help-darwin-amd64` | macOS Intel |
| `sap-help-linux-amd64` | Linux x64 |
| `sap-help.mcpb` | Claude Desktop extension with all of the above |

## Install (Claude Desktop)

**Extension:** Settings → Extensions → Advanced settings → Install Extension →
pick `sap-help.mcpb`. The binaries are not signed, so the dialog warns that the
extension is unverified.

**Binary:** put `sap-help.exe` somewhere permanent and add it to the
`mcpServers` section of `claude_desktop_config.json`
(Settings → Developer → Edit Config):

```json
"sap-help": {
  "command": "C:/Tools/MCP/sap-help.exe"
}
```

Use forward slashes or doubled backslashes in the path; one missing or extra
comma and Claude Desktop silently skips the whole file. Quit Claude Desktop from
the tray icon (closing the window does not stop servers) and start it again.

Check: ask Claude to call `sap_help` with `action=status`. The first line of
`%APPDATA%\Claude\logs\mcp-server-sap-help.log` shows the running version.

## The tool

| action | What it does | Parameters |
|---|---|---|
| `search` | Search help.sap.com | `query`, `product` (`SAP_ERP`, `SAP_S4HANA_ON_PREMISE`, `SAP_S4HANA_CLOUD`, `SUPPORT_CONTENT`), `version`, `limit` 1–20 |
| `read` | A whole help.sap.com page as markdown | `url` from a search result, `part` |
| `community_search` | SAP Community search with client-side ranking | `query`, `limit` 1–25, `min_kudos` 0–10 |
| `community_read` | A whole thread: question and every reply, accepted solution marked | `url` or message id, `part` |
| `status` | Version, endpoints, cache state | — |

Queries go to the portals in English; the tool description tells Claude to
translate the user's question into SAP terminology itself.

## SAP Community behind a Cloudflare check

From some networks (VPN exits in particular) community.sap.com answers with a
Cloudflare "verify you are human" check. A program cannot pass it, and this
server does not try to. Instead of an error, `community_search` and
`community_read` then return `status: browser_required` and a `javascript`
field:

1. Claude opens community.sap.com in the built-in browser of Claude Desktop.
2. If the check appears, the **user** ticks it — Claude is told never to.
3. Claude runs the returned script on that tab. It sends the same LiQL queries,
   ranks and formats the same way, and returns the same JSON.

After a Cloudflare check the server does not call the forum directly for
30 minutes. help.sap.com is not affected.

## Build

Go 1.22+ (the zip from go.dev/dl installs without admin rights). No external
modules, no network needed to build.

```powershell
go test ./...
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w" -o dist\sap-help.exe .
.\dist\sap-help.exe -version
```

All platforms and the `.mcpb`: `bash build.sh` (the `.mcpb` step needs `zip`).

## Releases

A release is built by GitHub Actions when a new version reaches `main`. The
version lives in three places — `serverVersion` in `main.go`, `version` in
`mcpb/manifest.json`, a section in `CHANGELOG.md` — and `build.sh` checks that
they agree.

## Notes

- HTML → markdown by a small built-in converter: headings, lists, links, code,
  tables. Very complex layouts may come out rougher.
- HTTP 403/429 from a portal is reported as blocking, and Claude is told not to
  retry in a loop.
- The help.sap.com endpoints are not publicly documented and may change.
- stdio only (no HTTP transport).

## License

[MIT](LICENSE). SAP and SAP product names are trademarks of SAP SE; this project
is not affiliated with SAP. All content reached through the server belongs to
SAP SE and the respective authors; the server stores and redistributes nothing.
