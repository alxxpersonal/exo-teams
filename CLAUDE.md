# Exo Teams

Go CLI for Microsoft Teams using the internal API. No admin consent, no IT department.

## Critical Rules

- ALWAYS handle token expiry gracefully - call `EnsureSkypeToken()` or `EnsureFresh()` before API calls
- ALWAYS use `url.PathEscape()` on conversation IDs and path parameters in URLs
- ALWAYS send status output to stderr, data to stdout
- NEVER commit tokens, credentials, or personal identifiers (emails, student IDs, tenant UUIDs)
- NEVER use `http.DefaultClient` - use the configured client with timeout on both `Client` and `GraphClient`

## Architecture

- Two HTTP client types: `Client` (internal Teams API, 30s timeout) and `GraphClient` (Graph API, 60s timeout)
- Auth uses device code OAuth flow - tokens stored at `~/.exo-teams/` with 0600 permissions
- Five token scopes: skype, chatsvcagg, teams, graph, assignments
- Internal API auth: `Authentication: skypetoken=<derived>` header (NOT `Authorization: Bearer`)
- Graph API auth: `Authorization: Bearer <token>` header
- Skypetoken is derived from the root skype JWT via the authz exchange endpoint
- Microsoft API returns wildly inconsistent types - use `any` for fields that vary

## Stack Decisions (Locked)

- Go 1.26+ with cobra for CLI
- Internal Teams API (`emea.ng.msg.teams.microsoft.com`) for messaging, not Graph
- Graph API only for: files/SharePoint, calendar, search, user profiles, assignments
- Assignments via `assignments.onenote.com` API (bypasses admin consent)
- No external dependencies beyond cobra

## Commands

```
make build         - build binary to ./exo-teams
make lint          - run go vet
make test          - run tests
go build -o exo-teams ./cmd/exo-teams/
```

## Implementation Pitfalls

- Conversation IDs contain colons and `@` signs - always URL-encode them
- `properties.files` in messages is a JSON string, not a JSON object - double-encoded
- AMS (attachment service) requires `X-Client-Version` and `ClientInfo` headers or rejects with 400
- File attachments in DMs use OneDrive upload + SharePoint share link, not AMS
- Chat `hidden` field is unreliable - most DMs have `hidden=true` but are active
- `annotationsSummary.emotions` can be either `[]any` or `map[string]any` depending on the message
- OneDrive files can return 423 (locked) after share link creation - retry with deduplicated filename

## Commit Style

Conventional commits: `type(scope): description`
NEVER add co-author tags.

## Compact Instructions

Always keep: current task context, file paths being edited, test results, API endpoint patterns, auth header formats.

## Do NOT

- Distribute Microsoft's proprietary API documentation or internal endpoint specs
- Store tokens in the repo or include them in error output
- Add telemetry, analytics, or phone-home features
- Use long dashes in comments or documentation - use "-" or commas
