# ![HAProxy](../assets/images/haproxy-weblogo-210x49.png "HAProxy")
## Kubernetes Controller

## Options

Multiple options can be combined

Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

// multiple options can be combined
controller, err := controller.New(opt.Option1(arg1), opt.Flag())
```

Available options:

| Function | Arguments |
| ---:|:--- |
| BackendNameTemplate | `template`(string) |
| CacheReSyncPeriod | `syncPeriod`(time.Duration) |
| ControllerConfCRD | `controllerConf`(types.NamespacedName) |
| ControllerName | `controllerName`(string) |
| DefaultsSectionName | `name`(string) |
| DisableIPv4 |  |
| DisableIPv6 |  |
| FrontendNameTemplate | `template`(string) |
| GatewayNsName | `gatewayNsName`(types.NamespacedName) |
| HaproxyConfChannel | `treeCh`(*ast.ChanType) |
| HaproxyDirs | `dirs`(haproxy.HaproxyDirs) |
| HugServiceLabel | `key`(string) |
| IPV4BindAddr | `addr`(string) |
| IPV6BindAddr | `addr`(string) |
| InitialStructured | `structuredCfg`(structured.Structured) |
| KubeConfig | `kubeconfig`(string) |
| LeaderElectionConfig | `leaderElectionEnabled`(bool) |
| LinkID | `template`(string) |
| Logging | `handlerType`(logging.LogHandlerType), `defaultLevel`(slog.Level), `logSettings`(*ast.MapType) |
| MetricsConfig | `metricsConfig`(config.MetricsConfig) |
| Namespaces | `namespaces`(*ast.ArrayType) |
| RuntimeUpdate | `timeout`(time.Duration) |
| ServerNameTemplate | `template`(string) |
| StartupSyncPeriod | `syncPeriod`(time.Duration) |
| StoreCertificateOnDisk | `structureType`(storage.StructureType) |
| StoreMapsOnDisk | `structureType`(storage.StructureType) |
| SyncPeriod | `syncPeriod`(time.Duration) |

### BackendNameTemplate


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.BackendNameTemplate(template))
```

### CacheReSyncPeriod


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.CacheReSyncPeriod(syncPeriod))
```

### ControllerConfCRD


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.ControllerConfCRD(controllerConf))
```

### ControllerName


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.ControllerName(controllerName))
```

### DefaultsSectionName


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.DefaultsSectionName(name))
```

### DisableIPv4


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.DisableIPv4())
```

### DisableIPv6


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.DisableIPv6())
```

### FrontendNameTemplate


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.FrontendNameTemplate(template))
```

### GatewayNsName


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.GatewayNsName(gatewayNsName))
```

### HaproxyConfChannel


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.HaproxyConfChannel(treeCh))
```

### HaproxyDirs


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.HaproxyDirs(dirs))
```

### HugServiceLabel


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.HugServiceLabel(key))
```

### IPV4BindAddr


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.IPV4BindAddr(addr))
```

### IPV6BindAddr


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.IPV6BindAddr(addr))
```

### InitialStructured


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.InitialStructured(structuredCfg))
```

### KubeConfig


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.KubeConfig(kubeconfig))
```

### LeaderElectionConfig


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.LeaderElectionConfig(leaderElectionEnabled))
```

### LinkID


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.LinkID(template))
```

### Logging


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.Logging(handlerType, defaultLevel, logSettings))
```

### MetricsConfig

ControllerPodConfig sets the ControllerPodConfig of the controller.

Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.MetricsConfig(metricsConfig))
```

### Namespaces


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.Namespaces(namespaces))
```

### RuntimeUpdate

RuntimeUpdate sets the option to perform runtime commands through the runtime socket
The timeout specifies the max time to wait for the HUG application to sen the runtime.Runtime
to the library at start up.

Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.RuntimeUpdate(timeout))
```

### ServerNameTemplate


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.ServerNameTemplate(template))
```

### StartupSyncPeriod


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.StartupSyncPeriod(syncPeriod))
```

### StoreCertificateOnDisk


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.StoreCertificateOnDisk(structureType))
```

### StoreMapsOnDisk


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.StoreMapsOnDisk(structureType))
```

### SyncPeriod


Example:
```go
import (
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate
  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options
)

controller, err := controller.New(opt.SyncPeriod(syncPeriod))
```

