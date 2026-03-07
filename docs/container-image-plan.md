# Plan: Simplify Container Image (K8s-first)

## Context

The current container image has Docker-era baggage: ~15 entrypoint.d scripts that translate ENV vars into config files, System Ruby + Gemfile for `puppet config set/print`, and build dependencies (gcc, make, ruby-devel). In K8s, the operator manages all config via ConfigMaps/Secrets, so this is unnecessary.

## Changes

### 1. Rewrite `images/openvoxserver/Containerfile`

**base stage** - remove Ruby:
- Keep: JDK 17, bash, curl, openssl, tar, gzip, findutils, hostname
- Remove: `ruby` (and `dnf module enable ruby:3.3`)

**build stage** - add Ruby temporarily:
- Add Ruby just for `install-vendored-gems.sh` (needs it during build)
- Move `puppetserver gem install openvox` here (JRuby, writes to /opt/puppetlabs)
- Move openvoxserver-ca patch here
- No Gemfile, no bundler, no gcc/make/ruby-devel

**final stage** - slim, no Ruby:
- COPY from build (no Ruby in final image)
- Remove: Gemfile COPY, gem/bundler install, build deps, entrypoint.d COPY
- Remove: `puppetdb.conf` COPY (operator creates via ConfigMap)
- Remove: `puppetserver` defaults COPY (inline in entrypoint.sh)
- Remove: all ENV vars for config (AUTOSIGN, CA_ENABLED, etc.)
- Keep: `puppet.conf` (minimal default for puppetserver to start)
- Keep: `conf.d/product.conf`, `conf.d/puppetserver.conf` (JRuby defaults)
- Keep: `logback.xml`, `request-logging.xml` (stdout logging)
- Keep: OpenShift random-UID pattern, HOME symlinks

### 2. Simplify `images/openvoxserver/entrypoint.sh`

- Remove entrypoint.d loop
- Inline defaults from `/etc/default/puppetserver` (JAVA_BIN, INSTALL_DIR, CONFIG, BOOTSTRAP_CONFIG)
- Use env var overrides (JAVA_ARGS)

### 3. Simplify `images/openvoxserver/healthcheck.sh`

- Remove `puppet config print` calls (no System Ruby)
- Use simple `curl --insecure` to localhost status endpoint
- K8s uses livenessProbe/readinessProbe anyway, this is just for Docker/Podman

### 4. Delete files

- `images/openvoxserver/entrypoint.d/` (entire directory, ~18 scripts)
- `images/openvoxserver/Gemfile`
- `images/openvoxserver/Gemfile.lock`
- `images/openvoxserver/puppetdb.conf` (operator creates via ConfigMap)
- `images/openvoxserver/puppetserver` (defaults inlined in entrypoint.sh)

### 5. Keep files unchanged

- `images/openvoxserver/conf.d/product.conf`
- `images/openvoxserver/conf.d/puppetserver.conf` (uses ENV vars for max-active-instances etc.)
- `images/openvoxserver/logback.xml` (stdout logging, K8s best practice)
- `images/openvoxserver/request-logging.xml`
- `images/openvoxserver/puppet.conf` (minimal paths config)

## Key Decisions

- **No System Ruby in final image**: operator manages certs via Secrets, `puppet ssl bootstrap` not needed. `puppetserver` CLI is bash + java.
- **`puppetserver gem install openvox`** moves to build stage (uses JRuby/java, not System Ruby). Version from build arg.
- **puppetserver.conf** keeps ENV var placeholders (`OPENVOXSERVER_MAX_ACTIVE_INSTANCES` etc.) - operator sets these.
- **All Servers use Deployments**: CA with Recreate strategy, compilers with RollingUpdate. No StatefulSets.
- **Shared cert per Server CR**: all pods of a Server share the same cert from a Secret. No per-pod certs.

## Verification

```bash
podman build -t openvoxserver:rootless images/openvoxserver/
# No Ruby in final image:
podman run --rm openvoxserver:rootless ruby --version  # should fail
# Java works:
podman run --rm openvoxserver:rootless java -version
# puppetserver CLI works:
podman run --rm openvoxserver:rootless puppetserver --help
```
