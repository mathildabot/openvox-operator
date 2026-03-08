# Certificate Signing

## Current Behavior

The operator supports `autosign` via `Environment.spec.ca.autosign`. When set to `"true"`, the CA server automatically signs all incoming CSRs. This is suitable for development and trusted environments.

For production use cases where manual or policy-based approval is required, a more granular signing mechanism is planned.

## ServiceAccount Model

Each Environment creates dedicated ServiceAccounts with minimal privileges:

| ServiceAccount | Purpose | K8s API Token |
|---|---|---|
| `{env}-ca-setup` | CA setup job: creates CA certs and writes the CA Secret | Yes (needs to create/update Secrets) |
| `{env}-server` | All server pods (CA + compiler) | No (`automountServiceAccountToken: false`) |

The operator itself runs with its own ServiceAccount (managed by the Helm chart) with cluster-wide RBAC.

## Planned: CertificateRequest CRD

### Problem

Without `autosign=true`, CSRs remain pending on the CA server until an administrator manually runs `puppetserver ca sign`. In Kubernetes, this should be handled declaratively through a CRD.

### Design

A new `CertificateRequest` CRD represents a pending CSR:

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: CertificateRequest
metadata:
  name: compiler-01
  namespace: openvox-system
spec:
  environmentRef: production
  certname: compiler-01.openvox-system.svc
status:
  phase: Pending  # Pending | Approved | Signed | Denied
  fingerprint: "AA:BB:CC:..."
  requestedAt: "2025-01-01T00:00:00Z"
  signedAt: null
```

### Architecture

The signing process runs as a **sidecar container** in the CA server pod:

```
CA Server Pod
+---------------------------+
| openvox-server            |  Main container: puppetserver (CA enabled)
|   - CA PVC (read-write)   |
|   - Port 8140             |
+---------------------------+
| ca-signing-agent          |  Sidecar: polls for pending CSRs, manages CertificateRequest CRDs
|   - CA PVC (shared)       |
|   - K8s API token (SA)    |
+---------------------------+
```

The sidecar shares the CA PVC with the main container and has access to the K8s API via a ServiceAccount token.

### Flow

```
1. Compiler starts
   |
   v
2. Init container sends CSR to CA server (puppet ssl bootstrap)
   |
   v
3. CSR lands on CA server filesystem (pending)
   |
   v
4. Sidecar polls `puppetserver ca list` periodically
   |
   v
5. Sidecar finds pending CSR
   -> Creates CertificateRequest CR (status: Pending)
   |
   v
6. Admin or policy controller sets status to Approved
   (kubectl, GitOps, OPA, custom webhook, etc.)
   |
   v
7. Sidecar detects Approved CertificateRequest
   -> Runs `puppetserver ca sign <certname>`
   -> Updates CertificateRequest status to Signed
   -> Updates CA Secret (refreshes CRL)
   |
   v
8. Compiler receives signed cert from CA server
   -> Main container starts
```

### RBAC Impact

When the sidecar is enabled, the CA server pod needs a different ServiceAccount:

| ServiceAccount | Purpose | K8s API Token |
|---|---|---|
| `{env}-ca-signing` | CA server pod with sidecar | Yes (needs CertificateRequest CRD access + Secret update) |

The `{env}-server` SA remains unchanged (no token) for compiler pods and CA pods without the sidecar.

### RBAC for the Signing Agent

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {env}-ca-signing
rules:
  # CertificateRequest CRDs
  - apiGroups: ["openvox.voxpupuli.org"]
    resources: ["certificaterequests"]
    verbs: ["get", "list", "watch", "create", "update", "patch"]
  - apiGroups: ["openvox.voxpupuli.org"]
    resources: ["certificaterequests/status"]
    verbs: ["get", "update", "patch"]
  # Update CA Secret (CRL refresh after signing)
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "update", "patch"]
```

### Configuration

The sidecar is opt-in via the Environment spec:

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Environment
metadata:
  name: production
spec:
  ca:
    autosign: "false"
    signing:
      enabled: true          # Enables the sidecar + CertificateRequest CRD flow
      pollInterval: 30s      # How often to check for pending CSRs
```

When `signing.enabled` is `false` (default) and `autosign` is `"false"`, CSRs must be signed manually (e.g., via `kubectl exec` into the CA pod).

### Denial and Revocation

- **Deny**: Setting a CertificateRequest to `Denied` triggers `puppetserver ca clean <certname>` (removes the pending CSR)
- **Revoke**: A separate `certificaterequests/revoke` subresource or a `revoked` status triggers `puppetserver ca revoke <certname>` and updates the CRL

### Implementation Phases

1. **Phase 1** (current): `autosign=true` for all environments
2. **Phase 2**: CertificateRequest CRD + sidecar for manual approval
3. **Phase 3**: Policy-based auto-approval (OPA/Kyverno integration, label-based rules)
4. **Phase 4**: CRL auto-refresh (sidecar updates CA Secret after any signing/revocation)
