# CertificateAuthority

A CertificateAuthority manages the CA infrastructure for an Environment: a PVC for CA data, a setup Job that runs `puppetserver ca setup`, and a Secret containing the public CA certificates.

## Example

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: CertificateAuthority
metadata:
  name: production-ca
spec:
  environmentRef: production
  storage:
    size: 1Gi
```

Autosigning is configured via [SigningPolicy](signingpolicy.md) resources that reference this CertificateAuthority.

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `environmentRef` | string | **required** | Reference to the Environment |
| `ttl` | string | `5y` | CA certificate TTL as duration string (e.g. `5y`, `365d`, `8760h`) |
| `allowSubjectAltNames` | bool | `true` | Allow SANs in CSRs |
| `storage` | [StorageSpec](index.md#storagespec) | - | PVC settings for CA data |
| `intermediateCA` | [IntermediateCASpec](#intermediatecaspec) | - | Intermediate CA configuration |

### IntermediateCASpec

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Activate intermediate CA mode |
| `secretName` | string | - | Secret containing ca.pem, key.pem, crl.pem |

## Status

| Field | Type | Description |
|---|---|---|
| `phase` | string | Current lifecycle phase |
| `caSecretName` | string | Name of the Secret containing `ca_crt.pem`, `ca_crl.pem`, `infra_crl.pem` |
| `conditions` | []Condition | `CAReady` |

## Phases

| Phase | Description |
|---|---|
| `Pending` | CertificateAuthority created, waiting for reconciliation |
| `Initializing` | CA setup Job is running |
| `Ready` | CA Secret created, Certificates can be signed |
| `Error` | CA setup failed |

## Created Resources

| Resource | Name | Description |
|---|---|---|
| PVC | `{name}-data` | Persistent storage for CA keys and data |
| ServiceAccount | `{name}-ca-setup` | Job ServiceAccount with permission to create the CA Secret |
| Role | `{name}-ca-setup` | Scoped to CA Secret creation |
| RoleBinding | `{name}-ca-setup` | Binds Role to ServiceAccount |
| Job | `{name}-ca-setup` | Runs `puppetserver ca setup`, creates CA Secret |
| Secret | `{name}` | Public CA data: `ca_crt.pem`, `ca_crl.pem`, `infra_crl.pem` |
