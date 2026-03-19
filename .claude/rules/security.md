---
paths: ["**/*.go"]
---

# Security Rules

- NEVER commit tokens, cookies, or credentials to the repository
- NEVER hardcode API endpoints - use constants in a dedicated file
- ALWAYS handle token expiry gracefully (detect 401, prompt re-auth)
- Token storage path: ~/.exo-teams/ - NEVER store tokens elsewhere
- NEVER log token values, even at debug level
- Sanitize all user-facing output - strip HTML from message content
