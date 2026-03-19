---
paths: ["**/*.go"]
---

# Go Conventions

- Standard goimports grouping: stdlib, external, local (blank line separators)
- Section separators: `// --- Section Name ---` with proper capitalization
- Exported functions MUST have doc comments starting with function name
- All API types use `json` struct tags
- Error messages lowercase: `fmt.Errorf("failed to fetch messages: %w", err)`
- NEVER panic. Return errors with `%w` wrapping.
- Use cobra command patterns: RunE with error returns, not Run with os.Exit
- Professional tone in all comments - no slang, no long dashes
- NEVER use long dash in comments or strings, use "-" or commas
