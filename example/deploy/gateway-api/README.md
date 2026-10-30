## Gateway API

HAProxy Unified Gateway currently supports v1.7.0 version, fot time being only specific routes are implemented:

- gateway.networking.k8s.io/v1
  - kind: HTTPRoute

## How to deploy

```sh
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.7.0/standard-install.yaml
```

## Upgrading from v1.6

Development builds now support the v1.7 CRD bundle instead of v1.6.
This upgrade preserves existing features; it does not implement new v1.7 features.

Migrate experimental session-persistence fields before reapplying HTTPRoutes:

- Move `sessionName` to `cookie.name`.
- Rename `cookieConfig` to `cookie`, keeping its existing settings.
- Supply `cookie: {}` for cookie persistence without a custom name.

For example:

```yaml
sessionPersistence:
  type: Cookie
  cookie:
    name: sticky-cookie
```

Without this migration, custom cookie names may be lost, resetting sticky sessions.
