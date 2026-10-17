# Pinned references

| File                     | What                               | Source                                   | Refresh                  |
| ------------------------ | ---------------------------------- | ---------------------------------------- | ------------------------ |
| `intervals-openapi.json` | Intervals.icu API, OpenAPI 3.0 spec | https://intervals.icu/api/v1/docs (Swagger UI at `/api/v1/docs/swagger-ui/index.html`) | `just refresh-icu-spec` |

The spec is the Intervals.icu API's own published description, pinned here so field names and
endpoints can be checked offline and diffed when the API changes. After a refresh, review the diff
for endpoints or fields `internal/icu` and `skill/references/intervals.md` rely on.
