---
paths: ["**/*_test.go"]
---

# Testing Conventions

- Every test MUST assert something meaningful. No assertion-free tests.
- NEVER use NotPanics as the sole assertion. Test actual state/output.
- PREFER table-driven tests over copy-paste test functions.
- Use `require` for fatal checks, `assert` for non-fatal.
- Test names describe the scenario: `TestFetchMessages_HandlesExpiredToken`
- Mock HTTP responses for API client tests, never hit real endpoints in CI.
