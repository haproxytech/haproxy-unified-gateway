# Echo Deployment

This example contains an example with:
- Gateway Listeners:
  - Exact: `offload.haproxy`
- Route:
  - Exact: `*.haproxy`
- MathType:
  -  Exact: `/api/`



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


### Failure

```sh
curl --header "Host: offload.haproxy" http://$GW_IP:31081/api/foo
curl --header "Host: offload.haproxy"  https://$GW_IP:31444/api/foo -k

curl --header "Host: other.haproxy" http://$GW_IP:31081/api/
curl --header "Host: other.haproxy"  https://$GW_IP:31444/api/ -k

```
