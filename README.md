# openvox-operator

The goal of this project is to build a Kubernetes Operator for running a complete OpenVox environment (Server, PuppetDB, and related components) with minimal effort on Kubernetes and OpenShift.

As a foundation, this repository provides rootless container images built on UBI9 — no ezbake, no root — that work with OpenShift's random UID assignment out of the box.

### Roadmap

- [x] Rootless OpenVox Server container image (UBI9)
- [ ] Rootless OpenVox DB container image (UBI9)
- [ ] Helm Chart for OpenVox environment deployment
- [ ] Kubernetes Operator for managing OpenVox environments (CA lifecycle, certificate distribution, scaling compilers, PuppetDB integration)

## Quick Start

### Build

```bash
cd images/openvoxserver
podman build -t openvoxserver:rootless .
```

### Run

```bash
# Default user (1001:0)
podman run --rm -p 8140:8140 -e USE_OPENVOXDB=false openvoxserver:rootless

# Random UID (OpenShift simulation)
podman run --rm -p 8140:8140 --user 12345:0 -e USE_OPENVOXDB=false openvoxserver:rootless
```

### Docker Compose

```bash
cd examples
docker compose up
```

### Kubernetes

```bash
kubectl apply -f examples/kubernetes/openvoxserver.yaml
```

## Architecture

### No ezbake

The upstream OpenVox Server uses ezbake for packaging, which generates init scripts that start as root and switch users via `runuser`/`su`. This breaks rootless containers and OpenShift's random UID assignment.

This image starts the JVM directly:

```bash
exec java ${JAVA_ARGS} \
    --add-opens java.base/sun.nio.ch=ALL-UNNAMED \
    --add-opens java.base/java.io=ALL-UNNAMED \
    -Dlogappender=STDOUT \
    -cp "${INSTALL_DIR}/puppet-server-release.jar" \
    clojure.main -m puppetlabs.trapperkeeper.main \
    --config "${CONFIG}" \
    --bootstrap-config "${BOOTSTRAP_CONFIG}"
```

### OpenShift Random UID

The image follows the [OpenShift random UID pattern](https://docs.openshift.com/container-platform/latest/openshift_images/create-images.html#use-uid_create-images):

- `USER 1001:0` (member of root group)
- All writable directories: `chgrp -R 0`, `chmod -R g=u`, SGID on directories
- `HOME` set explicitly to avoid `/` as home for random UIDs
- `manage_internal_file_permissions` set to `false` in puppet.conf

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `OPENVOXSERVER_JAVA_ARGS` | `-Xms1024m -Xmx1024m` | JVM memory settings |
| `OPENVOXSERVER_PORT` | `8140` | HTTPS listen port |
| `OPENVOXSERVER_HOSTNAME` | `` | Server hostname |
| `CERTNAME` | `` | Certificate name |
| `DNS_ALT_NAMES` | `` | Subject alternative names for the certificate |
| `AUTOSIGN` | `true` | Certificate autosigning (`true`/`false`/path) |
| `CA_ENABLED` | `true` | Run as CA (`true`) or compiler (`false`) |
| `CA_HOSTNAME` | `puppet` | CA server hostname (compiler mode) |
| `CA_PORT` | `8140` | CA server port |
| `USE_OPENVOXDB` | `true` | Enable PuppetDB integration |
| `OPENVOXDB_SERVER_URLS` | `https://openvoxdb:8081` | PuppetDB server URLs |
| `OPENVOXSERVER_MAX_ACTIVE_INSTANCES` | `1` | JRuby instances |
| `OPENVOXSERVER_ENVIRONMENT_TIMEOUT` | `unlimited` | Environment cache timeout |
| `ENVIRONMENTPATH` | `/etc/puppetlabs/code/environments` | Environment path |
| `INTERMEDIATE_CA` | `false` | Use intermediate CA |

## Project Structure

```
openvox-operator/
├── images/
│   └── openvoxserver/
│       ├── Containerfile
│       ├── entrypoint.sh
│       ├── entrypoint.d/
│       ├── healthcheck.sh
│       └── conf.d/
├── examples/
│   ├── docker-compose.yaml
│   └── kubernetes/
│       └── openvoxserver.yaml
├── LICENSE
└── README.md
```

## License

Apache License 2.0
