# Roadmap

## Completed

- [x] Rootless OpenVox Server container image (UBI9, tarball-based, no ezbake)
- [x] CRD data model design (Environment, Pool, Server, CodeDeploy, Database)
- [x] Initial Go operator scaffolding (go.mod, cmd/main.go, controller stubs)
- [x] Documentation: README.md, data-model.md, design.md, architecture.md

## Architecture Decisions

These decisions were made during design and should be followed during implementation:

- **All Servers use Deployments** - CA with Recreate strategy, compilers with RollingUpdate. No StatefulSets.
- **Shared cert per Server CR** - all pods of a Server share the same cert from a Secret. No per-pod certs.
- **Pool owns the Service** - solves ownership when multiple Servers share a Service
- **Environment creates CA Service automatically** - internal ClusterIP, no Pool needed for CA
- **No System Ruby in container image** - operator manages certs via Secrets, config via ConfigMaps
- **Never use em-dashes** - only normal hyphens everywhere

## Next: Container Image Simplification

See [container-image-plan.md](container-image-plan.md) for the detailed plan.

- [ ] Remove entrypoint.d scripts (config comes via ConfigMap)
- [ ] Remove Gemfile, bundler, System Ruby from final image
- [ ] Remove build dependencies (gcc, make, ruby-devel)
- [ ] Move `puppetserver gem install openvox` to build stage
- [ ] Simplify entrypoint.sh (remove entrypoint.d loop, inline defaults)
- [ ] Simplify healthcheck.sh (no `puppet config print`, use curl)
- [ ] Delete: entrypoint.d/, Gemfile, Gemfile.lock, puppetdb.conf, puppetserver defaults
- [ ] Remove all ENV vars for config (AUTOSIGN, CA_ENABLED, etc.)

## Next: Rewrite Go Types for Multi-CRD Model

The current Go code in `api/v1alpha1/` still has the old single-CRD `OpenVoxServer` type.
Needs complete rewrite to match the data model in [data-model.md](data-model.md).

- [ ] Rewrite `api/v1alpha1/openvoxserver_types.go` - split into separate files:
  - `environment_types.go` (Environment CRD)
  - `pool_types.go` (Pool CRD)
  - `server_types.go` (Server CRD)
  - `codedeploy_types.go` (CodeDeploy CRD)
- [ ] Update `api/v1alpha1/groupversion_info.go` to register new types
- [ ] Regenerate `api/v1alpha1/zz_generated.deepcopy.go`
- [ ] Delete old `openvoxserver_types.go`

## Next: Rewrite Controllers for Multi-CRD Model

The current controllers in `internal/controller/` still use the old single-CRD model.

- [ ] Rewrite `internal/controller/` - split into separate controllers:
  - `environment_controller.go` (manages ConfigMaps, CA Job, CA Secret, CA PVC, CA Service)
  - `pool_controller.go` (manages Kubernetes Service)
  - `server_controller.go` (manages Deployment, HPA, cert Secret)
  - `codedeploy_controller.go` (manages PVC, Job, CronJob)
- [ ] Update `cmd/main.go` to register new controllers and types
- [ ] Delete old controller files (openvoxserver_controller.go, configmap.go, ca.go, compiler.go)

## Next: CRD Manifests and RBAC

- [ ] Generate CRD YAML manifests in `config/crd/bases/`
- [ ] Create RBAC roles in `config/rbac/`
- [ ] Create sample CRs in `config/samples/`

## Later

- [ ] r10k code deployment (Job / CronJob with shared PVC)
- [ ] HPA for compiler autoscaling
- [ ] cert-manager intermediate CA support
- [ ] OLM bundle for OpenShift
- [ ] Rootless OpenVoxDB container image

## Outdated Files

These files exist but are outdated and need rewrite or deletion:

| File | Status |
|---|---|
| `api/v1alpha1/openvoxserver_types.go` | Old single-CRD model, needs rewrite |
| `api/v1alpha1/zz_generated.deepcopy.go` | Generated from old types, needs regeneration |
| `cmd/main.go` | Registers old OpenVoxServer type, needs update |
| `internal/controller/openvoxserver_controller.go` | Old single-CRD controller, needs rewrite |
| `internal/controller/configmap.go` | Old controller helper, needs rewrite |
| `internal/controller/ca.go` | Old controller helper, needs rewrite |
| `internal/controller/compiler.go` | Old controller helper, needs rewrite |
| `docs/design.md` | Partially outdated, still references old CRD in some places |
| `docs/architecture.drawio` | Outdated diagram, replaced by mermaid in README |
| `images/openvoxserver/entrypoint.d/` | To be deleted (K8s-first) |
| `images/openvoxserver/Gemfile` | To be deleted (no System Ruby) |
| `images/openvoxserver/Gemfile.lock` | To be deleted |
