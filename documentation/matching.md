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

## Matched rules that cannot forward

A rule that matches a request but has no `backendRefs` (omitted, or an
empty list) terminates the match: the request never falls through to a
less specific rule, on the same Route or on another one. By default such a
request gets a `404`, the same as an unmatched one.

`--gwapi-compat` (or `GWAPI_COMPAT=1`) switches that response to `500`,
the status the Gateway API conformance suite expects when a route cannot
reach any backend. Requests that match no rule stay `404` in both modes,
and a matching rule whose only action is a `RequestRedirect` filter keeps
redirecting: the flag only changes the status-code of the terminal
backendless rule.

Under the same flag, a rule whose `backendRefs` cannot be resolved
(missing Service, unsupported group/kind, or a cross-namespace reference
no `ReferenceGrant` permits) keeps its match and answers `500` for its
share of the traffic, instead of dropping the rule: valid sibling
backends keep their own weights, so mixed rules forward the valid
portion as before. Without the flag, any invalid `backendRef` drops the
rule from routing, like an unprogrammable match.

## TCPRoute

A `TCPRoute` attaches to a `TCP` protocol listener, and selection is by
port alone: of the routes attached to one listener, only the oldest
(then `{namespace}/{name}`) receives connections; the others stay
Accepted with no traffic, per the Gateway API tie-break. Weighted
`backendRefs` split connections randomly, by weight.

A TCP port with no route, or a share that resolves to no backend, closes
the connection: a TCP client must not receive an HTTP error page. Under
`--gwapi-compat`, an invalid `backendRef` keeps its weight as rejected
connections -- weighted `80`, it rejects roughly `80%` of them -- while
without the flag it is treated as absent and the remaining backends take
its share; a route whose refs are all invalid rejects every connection
in both modes.

Conformance deployments enable this flag on every generated controller (see
[conformance.md](conformance.md)).

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

`PathPrefix` matches at path-element boundaries: a prefix is stored with a
trailing `/` and the lookup input gains one too, so `/v2` matches `/v2` and
`/v2/...` but not `/v2example`. This holds for the native lookup and for
`lua.find_route`/`lua.select_route` alike.

Known divergences from the spec (also tracked in the repository docs):

- hostname precedence vs path specificity ordering.
