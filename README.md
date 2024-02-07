# OVHCLOUD NASHA KUBERNETES OPERATOR

## Introduction

NAS-HA are reduncdant servers over a ZFS Storage, exposed using NFS.

Each partition has an IP access list, empty on creation. You have to add your OVHCLOUD service ( Dedicated Server, Public Cloud Instance, etc ... ) IPv4 address to this list to allow partition mounting.

When using NFS on Kubernetes, each node ExternalIp must be added to this list. Due to the nature of kubernetes clusters ( ephemeral nodes and autoscaling ).

This operator runs as DaemonSet on every nodes, registering the node's External IP on the configured partition at runtime, unregistering on exit.

### Private networks

When using private networks with gateway, all outgoing node traffic is flowing thru the private interface via the gateway. This is nice for security concerns, and having a single source IP address on your cluster is convenient.

The NAS-HA acces list only authorizes nodes public Ips', you cannot join them from private networks, you have to route the traffic thru the public interface.

The route-fixer DaemonSet detects routing to the NAS-HA ip and adds a route if needed.

## Installation

This operator requires an OVH API access, create one on <https://www.ovh.com/auth/api/createToken> with unlimited validity and GET+POST+DELETE rights on `/dedicated/nasha/*`

NAS-HA partition definition needs 4 parameters, `name` (the name of the partition), `ip` (the NAS-ha IPv4), `nasha` (the NAS-HA name, something like `zpool-xxxxx`) and `exclusive` (boolean, cleanup all unknown accesses on operator on start).

### Helm

```sh
# create the secret first
kubectl apply -f - << EOF
apiVersion: v1
kind: Secret
metadata:
  name: nasha-ovh.conf
  namespace: ovh-system
type: Opaque
stringData:
  endpoint: ovh-eu
  application_key: xxxx
  application_secret: xxxx
  consumer_key: xxxx
EOF

helm repo add ovh-nasha-operator https://vixns.github.io/ovh-nasha-operator/chart/
helm install ovh-nasha-operator ovh-nasha-operator \
  --repo https://helm.vixns.net \
  --namespace ovh-system \
  --set partitions[0].name=mypartition \
  --set partitions[0].nasha=zpool-xxxx \
  --set partitions[0].ip=xxx.xxx.xxx.xxx \
  --set partitions[0].exclusive=false
```

