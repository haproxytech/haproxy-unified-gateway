# HTTPRoute match conditions

How HUG translates `HTTPRoute` match conditions (method, headers, query
params) into HAProxy routing. Path and hostname matching are covered by the
[conformance](conformance.md) tests and behave as the Gateway API specifies.

## Supported match types

| Match | Types | Conformance |
|---|---|---|
| `path` | `Exact`, `PathPrefix`, `RegularExpression` | Core |
| `headers` | `Exact`, `RegularExpression` | Core (`Exact`), implementation-specific (`RegularExpression`) |
| `method` | exact method name (e.g. `GET`) | Extended |
| `queryParams` | `Exact`, `RegularExpression` | Extended |

## Semantics

- **Header names** are case-insensitive. When a match repeats a header name
  (case-insensitively), only the first entry counts; later duplicates are
  ignored, as the spec requires.
- **Header values** are compared against the full field value of each
  occurrence: a repeated header matches when **any** occurrence matches. No
  comma-joining.
- **Query parameter names** are case-sensitive (unlike header names).
- **Query parameter values** are compared after percent-decoding, with `+`
  decoded as a space. Extra parameters in the request are ignored. Only exact
  (whole-value) matching is supported for `Exact`.
- **Method** is compared exactly: a `HEAD` request does not match a `GET`
  rule.
- **`RegularExpression`** uses a PCRE2-compatible dialect and performs an
  **unanchored search**: anchor with `^...$` when the whole value must match.
  The regex is validated at admission with the Go (RE2) engine, so the
  supported subset excludes PCRE-only constructs such as lookarounds
  (`(?=...)`, `(?!...)`) and backreferences. A value that fails this
  validation rejects its rule with an `UnsupportedValue` condition.

## Precedence

When several rules match a request, HUG applies the Gateway API precedence
order, continuing on ties:

1. `Exact` path match.
2. `PathPrefix` match with the largest number of characters.
3. Method match.
4. Largest number of header matches.
5. Largest number of query-param matches.
6. Oldest Route (creation timestamp), then `{namespace}/{name}` order.
7. First matching rule in the rule list.

Ties on (1)-(5) within the same entry are broken by (7). A rule with
conditions that does not match does **not** shadow less specific rules: if a
longer prefix fails its conditions, shorter prefixes are still tried, and a
request matching nothing gets a `404` (no fallback to a less specific rule
outside the precedence order).

Point 6 is currently approximated: ties across routes on the same hostname
are broken by alphabetical `{namespace}/{name}` order instead of creation
timestamp first.

## Status conditions

Rules whose match conditions cannot be programmed are dropped:

- All rules invalid → `Accepted: False`, reason `UnsupportedValue`.
- Some rules invalid → `PartiallyInvalid: True`, reason `UnsupportedValue`,
  message prefixed with `Dropped Rule`.

This covers invalid regexes and rules whose encoded conditions exceed the
map-value size limit (~16KB per HAProxy map value): a single rule is capped
at 4096 bytes and a whole route at 12288 bytes of encoded conditions.

## Implementation notes

Conditions are not separate HAProxy ACLs: they travel inside the runtime
map values as an ordered candidate list (`~1;conds>target;...`), so route
changes still apply without a reload. `lua.select_route` (single-candidate
fast path) and `lua.find_route` (multi-candidate) evaluate the candidates
against the request in the precedence order above.

Routes without conditions keep the legacy map-value format and are resolved
natively without any Lua on the request path.

Known divergences from the spec (also tracked in the repository docs):

- hostname precedence vs path specificity ordering,
- `PathPrefix` matches by characters, not path elements: a request to
  `/v2example` matches the prefix `/v2`, where the spec requires element
  boundaries. This is why the `HTTPRouteMatching` conformance suite stays
  skipped; the four other match suites run and pass.
