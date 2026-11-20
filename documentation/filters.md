# HTTPRoute filter values

HUG writes filter values into the HAProxy configuration shared by all routes.
A value is always literal text. HUG never evaluates it as a HAProxy expression.

## Header values

`set` and `add` values of `RequestHeaderModifier` and `ResponseHeaderModifier`
are sent as written. A value such as `%[src]` or `${HOME}` reaches the peer as
that exact text.

A value must not hold a control character. A tab is allowed.

## Allowed characters

| Value | Allowed besides letters and digits |
|---|---|
| Header name (`set`, `add`, `remove`, CORS header lists) | ``!&*+-.^_`\|~`` |
| `replaceFullPath`, `replacePrefixMatch` | `-._~!$&*+;=:@/%` |
| Hostname (`URLRewrite`, `RequestRedirect`) | `-.` |
| CORS origin | `-.:/*` |

A header name must not be empty.

Percent-encode any other character of a path. A space is `%20`.

A rule that uses `replacePrefixMatch` holds its `PathPrefix` match values to
the path characters above.

## Status conditions

A rule with a value outside these rules is dropped:

- All rules invalid: `Accepted: False`, reason `UnsupportedValue`.
- Some rules invalid: `PartiallyInvalid: True`, reason `UnsupportedValue`,
  message prefixed with `Dropped Rule`.

The message names the filter and the value.
