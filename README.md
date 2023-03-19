# OVHCLOUD NASHA KUBERNETES OPERATOR

## Introduction

NAS-HA are reduncdant servers over a ZFS Storage, exposed using NFS.

Each partition has an IP access list, empty on creation. You have to add your OVHCLOUD service ( Dedicated Server, Public Cloud Instance, etc ... ) IPv4 address to this list to allow partition mounting.

When using NFS on Kubernetes, each node ExternalIp must be added to this list. Due to the nature of kubernetes clusters ( ephemeral nodes and autoscaling ), this burden must be automated.

This operator runs as DaemonSet on every nodes, registering the node's External IP on the configured partition at runtime, unregistering on exit.

### Private networks

When using private networks with gateway, all outgoing node traffic is flowing thru the private interface thru the gateway. This is nice for security concerns, and having a single source IP addess on yuour cluster.

The NAS-HA acces list only authorise Public Ip, you cannot join them from private network, yous have to route the traffic thru the public interface.

The route-fixer DaemonSet detects routing to the NAS-HA ip and adds a route if needed.

## Installation

This operator requires an OVH API access, create one on <https://www.ovh.com/auth/api/createToken> with unlimited validity and GET+POST+DELETE rights on `/dedicated/nasha/*`

NAS-HA partitions are definded using 4 parameters, `name` (the name of the partition), `ip` (the NAS-ha IPv4), `nasha` (the NAS-HA name, something like `zpool-xxxxx`) and `exclusive` (boolean, cleanup all unknown accesses on operator on start).

### Helm

```sh
helm upgrade --install ovh-nasha-operator ovh-nasha-operator \
  --repo https://helm.vixns.net \
  --namespace ovh-system --create-namespace  \
  --values -<<EOF
ovh.api.token:
  endpoint: ovh-eu
  application_key: xxxx
  application_secret: xxxx
  consumer_key: xxxx
cm:
  partitions:
    - name: my-partition
      nasha: zpool-xxxx
      ip: zxxx.xxx.xxx.xxx
      exclusive: false
EOF
```
