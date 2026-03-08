# Examples

## Minimal - Single Pod

A single Server with both CA and server role enabled. Suitable for development and lab environments.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Environment
metadata:
  name: lab
spec:
  image:
    repository: ghcr.io/slauger/openvox-server
    tag: "8.12.1"
  ca:
    autosign: "true"
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: puppet
spec:
  environmentRef: lab
  ca: true
  server: true
  certname: puppet
  dnsAltNames:
    - puppet
    - puppet-ca
  replicas: 1
```

## Production - CA + Server Pool + Canary

Separate CA server, a stable server pool with 3 replicas, and a canary server running a newer version. A Pool with a selector distributes traffic across all servers with the `server` role.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Environment
metadata:
  name: production
spec:
  image:
    repository: ghcr.io/slauger/openvox-server
    tag: "8.12.1"
  ca:
    autosign: "true"
    storage:
      size: 1Gi
  puppetdb:
    serverUrls:
      - https://openvoxdb:8081
  puppet:
    environmentTimeout: unlimited
    storeconfigs: true
    storeBackend: puppetdb
    reports: puppetdb
  code:
    claimName: puppet-code
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
spec:
  environmentRef: production
  selector:
    openvox.voxpupuli.org/role: server
  service:
    type: LoadBalancer
    port: 8140
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: ca
spec:
  environmentRef: production
  ca: true
  server: true
  certname: puppet
  dnsAltNames:
    - puppet
    - puppet-ca
  replicas: 1
  resources:
    requests:
      cpu: 500m
      memory: 1Gi
    limits:
      cpu: "2"
      memory: 2Gi
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: stable
spec:
  environmentRef: production
  replicas: 3
  maxActiveInstances: 2
  resources:
    requests:
      cpu: "1"
      memory: 2Gi
    limits:
      cpu: "4"
      memory: 4Gi
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: canary
spec:
  environmentRef: production
  image:
    tag: "8.13.0"
  replicas: 1
  resources:
    requests:
      cpu: "1"
      memory: 2Gi
    limits:
      cpu: "4"
      memory: 4Gi
```
