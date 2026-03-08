# Certificate

A Certificate manages the lifecycle of a single X.509 certificate signed by a CertificateAuthority.

## Example

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Certificate
metadata:
  name: production-cert
spec:
  authorityRef: production-ca
  certname: puppet
  dnsAltNames:
    - puppet
    - production-ca
```

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `authorityRef` | string | **required** | Reference to the CertificateAuthority |
| `certname` | string | `puppet` | Certificate common name (CN) |
| `dnsAltNames` | []string | - | DNS subject alternative names |

## Status

| Field | Type | Description |
|---|---|---|
| `phase` | string | Current lifecycle phase |
| `secretName` | string | Name of the Secret containing `cert.pem` and `key.pem` |
| `conditions` | []Condition | `CertSigned` |

## Phases

| Phase | Description |
|---|---|
| `Pending` | Waiting for CertificateAuthority to reach `Ready` |
| `Requesting` | Certificate signing Job is running |
| `Signed` | TLS Secret created, Servers can mount it |
| `Error` | Certificate signing failed |

## Signing Strategy

The controller uses two paths to obtain a signed certificate:

| Strategy | Condition | How it works |
|---|---|---|
| **CA setup export** | Certificate created before/with CA | CA setup Job creates the CA AND exports the server cert+key as a TLS Secret. The Certificate controller adopts the Secret. |
| **HTTP bootstrap** | Certificate created after CA is Ready | Job runs `puppet ssl bootstrap` against the running CA Service |

The controller discovers the CA Service automatically by finding Servers with `ca: true` in the same Environment and the Pools whose selector matches them.

## Created Resources

| Resource | Name | Description |
|---|---|---|
| ServiceAccount | `{name}-cert-setup` | Job ServiceAccount with permission to create the TLS Secret |
| Role | `{name}-cert-setup` | Scoped to TLS and CA Secret access |
| RoleBinding | `{name}-cert-setup` | Binds Role to ServiceAccount |
| Job | `{name}-cert-setup` | Signs the certificate via HTTP bootstrap and creates the TLS Secret |
| Secret | `{name}-tls` | Certificate data: `cert.pem`, `key.pem` |
