# Code Deployment

Puppet code (modules, Hiera data, environments) must be available on Server pods at `/etc/puppetlabs/code/environments`. The operator supports two methods to provide code: **OCI image volumes** and **PVCs**.

Code is only mounted on pods with `server: true`. CA-only pods are not affected by code changes.

## OCI Image Volumes (recommended)

Package Puppet code as an OCI image and reference it in the Config. The operator mounts it as a read-only [image volume](https://kubernetes.io/docs/concepts/storage/volumes/#image) directly into the pod.

**Requirements:** Kubernetes 1.31+ with the `ImageVolume` feature gate enabled.

### Building a Code Image

Create a `Containerfile` that copies your Puppet environments into the image:

```dockerfile
FROM scratch
COPY environments/ /etc/puppetlabs/code/environments/
```

Build and push:

```bash
docker build -t ghcr.io/example/puppet-code:v1.0.0 -f Containerfile .
docker push ghcr.io/example/puppet-code:v1.0.0
```

A typical control repository layout:

```
control-repo/
  environments/
    production/
      manifests/
      modules/
      hiera.yaml
      data/
```

### Configuring the Config

Set `code.image` on the Config to apply the code to all Servers:

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Config
metadata:
  name: production
spec:
  image:
    repository: ghcr.io/slauger/openvox-server
    tag: "8.12.1"
  code:
    image: ghcr.io/example/puppet-code:v1.0.0
```

### Pull Policy

Control when the image is pulled via `imagePullPolicy`. Defaults to `IfNotPresent`:

```yaml
spec:
  code:
    image: ghcr.io/example/puppet-code:v1.0.0
    imagePullPolicy: Always
```

Supported values: `Always`, `IfNotPresent`, `Never`.

### Digest References

For immutable, reproducible deployments you can reference images by digest instead of (or in addition to) a tag:

```yaml
spec:
  code:
    image: ghcr.io/example/puppet-code@sha256:45b23dee08af5e43a7fea6c4cf9c25ccf269ee113168c19722f87876677c5cb2
```

A tag+digest combination also works:

```yaml
spec:
  code:
    image: ghcr.io/example/puppet-code:v1.0.0@sha256:45b23dee08af5e43a7fea6c4cf9c25ccf269ee113168c19722f87876677c5cb2
```

### Rolling Out Code Changes

Update the image reference to deploy new code. The operator detects the change and triggers a rolling restart of all Server pods:

```yaml
spec:
  code:
    image: ghcr.io/example/puppet-code:v1.1.0
```

### Private Registries

For private registries, create a pull secret and reference it:

```yaml
spec:
  code:
    image: registry.example.com/puppet-code:v1.0.0
    imagePullSecret: registry-credentials
```

### Per-Server Override

A Server can override the Config's code source. This is useful for testing new code on a canary server:

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: canary
spec:
  configRef: production
  certificateRef: canary-cert
  code:
    image: ghcr.io/example/puppet-code:v2.0.0-rc1
```

## PVC

Reference an existing PVC containing Puppet code. The PVC must be pre-populated externally (e.g. by a CI/CD pipeline or a CronJob running r10k).

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Config
metadata:
  name: production
spec:
  image:
    repository: ghcr.io/slauger/openvox-server
    tag: "8.12.1"
  code:
    claimName: puppet-code
```

The PVC must contain the environments directory at `/etc/puppetlabs/code/environments`.

| Setup | Access Mode | Requirement |
|---|---|---|
| Single-node | RWO | Any storage provider |
| Multi-node | RWX | NFS, CephFS, EFS, Longhorn, etc. |

## Comparison

| | OCI Image Volume | PVC |
|---|---|---|
| **Immutability** | Code is immutable per image tag | Mutable, can change at any time |
| **Rollout** | Automatic rolling restart on image change | Manual restart or `environmentTimeout` |
| **Versioning** | Container registry tags | External (Git, CI/CD) |
| **Multi-node** | No RWX needed | Requires RWX for multi-node |
| **Kubernetes version** | 1.31+ | Any |
| **Use case** | Production, GitOps workflows | Legacy setups, external tooling |
