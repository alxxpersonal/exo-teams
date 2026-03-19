---
name: code-conventions
description: Code style and conventions for exo-teams. Use this skill when writing, reviewing, or modifying any Go code in this repository. Applies to all code generation, refactoring, and review tasks.
---

# Code Conventions

Follow these conventions when writing or modifying code in this repository.

## Go

### Package Comments

Every Go package should have a comment if it exports symbols.

### Imports

Use standard goimports grouping (stdlib, external, local) with blank line separators:

```go
import (
    "encoding/json"
    "fmt"
    "net/http"

    "github.com/spf13/cobra"

    "github.com/alxxpersonal/exo-teams/internal/api"
    "github.com/alxxpersonal/exo-teams/internal/auth"
)
```

### Section Separators

Use dashed comments for major code sections. Section names use proper capitalization:

```go
// --- Messages ---

// --- Commands ---

// --- Helpers ---
```

### Comments

Use standard Go doc comment conventions. Professional tone, proper capitalization. Exported functions must have comments starting with the function name:

```go
// FetchMessages retrieves messages from a Teams channel or chat.
func (c *Client) FetchMessages(chatID string, limit int) ([]Message, error) {
```

Rules:
- Professional language only, no slang or casual tone in comments
- NEVER use long dashes in comments or strings
- Only add comments where logic is not self-evident

### Struct Tags

Always use `json` tags on API types:

```go
type Message struct {
    ID          string `json:"id"`
    Content     string `json:"content"`
    From        string `json:"from"`
    ComposeTime string `json:"composetime"`
}
```

### Error Handling

Return errors, never panic. Use `fmt.Errorf` with `%w` for wrapping:

```go
if err != nil {
    return nil, fmt.Errorf("failed to fetch messages: %w", err)
}
```

### CLI Commands (Cobra)

- Use `RunE` with error returns, not `Run` with `os.Exit`
- Keep command definitions thin, delegate logic to internal packages
- Flags should have short and long forms where sensible

### API Client

- Do NOT use Microsoft Graph API (internal Teams API is intentional)
- Exception: files, calendar, search, and assignments features may use Graph API
- All endpoints as constants, never hardcoded inline
- Handle HTTP 401 by prompting token refresh
- Strip HTML from message content before displaying to user

## General

### Commits

Conventional commits: `type(scope): description`

Types: `feat`, `fix`, `refactor`, `docs`, `infra`, `test`, `chore`

Example: `feat(messages): add reply support for channel messages`

NEVER add co-author tags.

### Engineering Philosophy

Do not over-engineer. Keep solutions simple and direct. Add abstraction only when a clear repeated pattern emerges, not preemptively.
