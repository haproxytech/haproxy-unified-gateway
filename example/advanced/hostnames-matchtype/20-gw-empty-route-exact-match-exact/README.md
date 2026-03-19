# Echo Deployment

This example contains an example with:
- Gateway Listeners:
  - Empty
- Route:
  - Exact: `offload.haproxy`
- MatchType:
  - Exact: `/api/`

## How to deploy

```sh
kubectl apply -f .
```

## How to test e2e curls from your computer

### Success


```sh
GW_IP=$(kubectl get gateway hug-gateway -n default -o jsonpath='{.status.addresses[0].value}')
curl --header "Host: offload.haproxy" http://$GW_IP:31081/api/
curl --header "Host: offload.haproxy"  https://$GW_IP:31444/api/ -k

```
