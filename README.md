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

### Configuration

This operator require an OVH API access, create one on <https://www.ovh.com/auth/api/createToken> with unlimited validity and GET+POST+DELETE rights on `/dedicated/nasha/*`

Then, create a secret with these tokens

```sh
OVH_ENDPOINT=ovh-eu
OVH_APPLICATION_KEY=xxxxxxx
OVH_APPLICATION_SECRET=xxxxxxx
OVH_CONSUMER_KEY=xxxxxxx
kubectl apply -f - <<EOF
apiVersion: v1
data:
  endpoint: $(echo -n "${OVH_ENDPOINT}" | base64 -w 0)
  key: $(echo -n "${OVH_APPLICATION_KEY}" | base64 -w 0)
  secret: $(echo -n "${OVH_APPLICATION_SECRET}" | base64 -w 0)
  consumer: $(echo -n "${OVH_CONSUMER_KEY}" | base64 -w 0)
kind: Secret
metadata:
  name: nasha-ovh.conf
  namespace: kube-system
EOF
```

Add the NAS-HA list in a config map

NAS-HA partitons are definded using 4 parameters, `name`, `ip` (the NAS-ha IPv4), `nasha` (the NAS-HA name, something like `zpool-xxxxx`) and `exclusive` (boolean, cleanup all accesses on operator on start).

You can add any number of partitions as a serialized json array:

```sh
kubectl apply -f - <<EOF
apiVersion: v1
data:
  partitions.json: '[{"ip":"xxx.xxx.xxx.xxx","nasha":"zpool-XXXXX","name":"my-partition","exclusive":true}]'
kind: ConfigMap
metadata:
  name: ovh-nasha
  namespace: kube-system
EOF
```

Continue installation using the helm chart

```sh
helm upgrade --install ovh-nasha-operator ovh-nasha-operator \
  --repo https://nexus.vixns.net/repository/helm/ \
  --namespace kube-system
```
