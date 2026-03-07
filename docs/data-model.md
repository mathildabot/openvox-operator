# Datenmodell — CRD Design

## Komponenten im OpenVox-Ökosystem

| Komponente | Was sie tut | Stateful? | Skalierbar? |
|---|---|---|---|
| **CA** | Zertifikate ausstellen, CRL verwalten | Ja (CA-Daten auf PVC) | Nein (genau 1) |
| **Compiler** | Kataloge kompilieren, Agent-Requests beantworten | Nein (stateless) | Ja (1–N) |
| **r10k** | Puppet-Code aus Git deployen | Nein (Job) | Nein |
| **PuppetDB** | Facts, Reports, Exported Resources speichern | Ja (PostgreSQL) | Begrenzt |
| **PostgreSQL** | Backend für PuppetDB | Ja | Ja (HA) |

## Beziehungen

```
                    ┌──────────────┐
                    │  OpenVoxCA   │
                    │              │
                    │ - Setup Job  │
                    │ - StatefulSet│
                    │ - PVC        │
                    └──────┬───────┘
                           │
                    produces CA Secret
                           │
              ┌────────────┼────────────┐
              ▼            ▼            ▼
     ┌────────────┐ ┌────────────┐ ┌────────────────┐
     │ OpenVox    │ │ OpenVox    │ │ OpenVoxPuppetDB │
     │ Compiler   │ │ Compiler   │ │ (future)        │
     │ Pool "web" │ │ Pool "ci"  │ │                 │
     └─────┬──────┘ └─────┬──────┘ └────────┬───────┘
           │               │                 │
           │  mounts code  │  mounts code    │
           │       ▼       │       ▼         │
           │  ┌────────┐   │  ┌────────┐     │
           │  │OpenVox │   │  │OpenVox │     │
           │  │R10k    │   │  │R10k    │     │
           │  │"prod"  │   │  │"staging│     │
           │  └────────┘   │  └────────┘     │
           │               │                 │
           └───────────────┴─────────────────┘
                           │
                    queries/stores
                           ▼
                    ┌──────────────┐
                    │  PuppetDB    │
                    │  (external)  │
                    └──────────────┘
```

## CRDs

### 1. `OpenVoxCA`

Die Certificate Authority. Genau eine pro Umgebung.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: OpenVoxCA
metadata:
  name: production-ca
spec:
  image:
    repository: ghcr.io/slauger/openvoxserver
    tag: "8.12.1"
  certname: "puppet"
  dnsAltNames:
    - puppet
    - puppet-ca
    - puppet-ca.openvox.svc
  ttl: 157680000
  allowSubjectAltNames: true
  autosign: "true"
  storage:
    size: 1Gi
  resources:
    requests: { memory: "1Gi", cpu: "500m" }
    limits:   { memory: "2Gi" }
  javaArgs: "-Xms512m -Xmx1024m"
  # Optional: intermediate CA
  # intermediateCA:
  #   secretName: my-intermediate-ca
status:
  phase: Running         # Pending | Setup | Running | Error
  ready: true
  secretName: production-ca-certs   # auto-generated Secret name
  serviceName: production-ca        # auto-generated Service name
  conditions:
    - type: Initialized
      status: "True"
    - type: Ready
      status: "True"
```

**Erzeugt**:
- CA Setup Job (einmalig)
- CA StatefulSet (replicas: 1)
- CA Service (`<name>:8140`)
- CA Secret (ca_crt.pem, ca_key.pem, ca_crl.pem)
- ConfigMaps (puppet.conf, webserver.conf für CA-Rolle)
- PVC für CA-Daten

### 2. `OpenVoxR10k`

Code-Deployment aus Git. Unabhängig von CA und Compilern.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: OpenVoxR10k
metadata:
  name: production-code
spec:
  image:
    repository: ghcr.io/slauger/r10k
    tag: "latest"
  sources:
    - name: production
      remote: https://github.com/example/control-repo.git
      basedir: /etc/puppetlabs/code/environments
  # Git auth (optional)
  # gitSecret: r10k-git-credentials
  schedule: "*/5 * * * *"       # CronJob für periodisches Update
  volume:
    size: 5Gi
    accessMode: ReadWriteOnce   # oder ReadWriteMany für Multi-Node
    # storageClass: ""
    # existingClaim: ""
status:
  phase: Ready           # Pending | Deploying | Ready | Error
  lastDeployTime: "2026-03-07T22:00:00Z"
  pvcName: production-code      # auto-generated PVC name
  conditions:
    - type: VolumeReady
      status: "True"
    - type: LastDeploySucceeded
      status: "True"
```

**Erzeugt**:
- PVC für Code
- Job (initial deploy)
- CronJob (periodischer Sync)

### 3. `OpenVoxCompiler`

Ein Pool von Puppet-Compilern. Kann mehrfach existieren (z.B. verschiedene Tiers).

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: OpenVoxCompiler
metadata:
  name: production-compilers
spec:
  image:
    repository: ghcr.io/slauger/openvoxserver
    tag: "8.12.1"

  # Referenz auf CA
  caRef:
    name: production-ca         # Name der OpenVoxCA CR

  # Referenz auf Code (optional, kann auch ein existierendes PVC sein)
  codeRef:
    name: production-code       # Name der OpenVoxR10k CR
    # Alternativ:
    # existingPVC: my-code-pvc

  # PuppetDB Verbindung (extern)
  puppetdb:
    serverUrls:
      - https://openvoxdb:8081

  replicas: 2
  autoscaling:
    enabled: false
    minReplicas: 1
    maxReplicas: 5
    targetCPU: 75

  dnsAltNames:
    - puppet
    - puppet.openvox.svc

  puppet:
    environmentTimeout: unlimited
    storeconfigs: true
    storeBackend: puppetdb
    reports: puppetdb
    extraConfig: {}

  resources:
    requests: { memory: "1Gi", cpu: "500m" }
    limits:   { memory: "2Gi" }
  javaArgs: "-Xms512m -Xmx1024m"
  maxActiveInstances: 2

status:
  phase: Running
  ready: 2
  desired: 2
  serviceName: production-compilers
  conditions:
    - type: SSLBootstrapped
      status: "True"
    - type: Ready
      status: "True"
```

**Erzeugt**:
- Deployment (replicas: N)
- Service (`<name>:8140`)
- HPA (wenn autoscaling.enabled)
- ConfigMaps (puppet.conf, puppetdb.conf, webserver.conf für Compiler-Rolle)
- CA-Disabled ConfigMap (ca.cfg)
- Pod-Affinity zu r10k (wenn RWO)

**Liest von anderen CRs**:
- `caRef` → holt Service-Name und CA-Secret von `OpenVoxCA.status`
- `codeRef` → holt PVC-Name von `OpenVoxR10k.status`

### 4. `OpenVoxPuppetDB` (Zukunft)

Für den Fall dass man PuppetDB auch vom Operator managen lassen will.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: OpenVoxPuppetDB
metadata:
  name: production-db
spec:
  image:
    repository: ghcr.io/slauger/openvoxdb
    tag: "8.12.1"
  caRef:
    name: production-ca
  postgresql:
    host: postgres.db.svc
    port: 5432
    database: puppetdb
    credentialsSecret: puppetdb-postgres-credentials
```

Erstmal **out of scope** — PuppetDB und PostgreSQL kommen extern. Aber das Datenmodell ist vorbereitet.

## Warum diese Aufteilung?

### Eine CRD für alles (vorher)

```yaml
OpenVoxServer     # CA + Compiler + r10k + PuppetDB — alles in einem
```

Probleme:
- Monolith-Spec, wird riesig
- Kann nur eine Compiler-Konfiguration haben
- r10k ist fest an den Server gekoppelt
- Schwer erweiterbar (PuppetDB managen?)

### Getrennte CRDs (neu)

```
OpenVoxCA          →  1 pro Cluster/Umgebung
OpenVoxR10k        →  1 pro Code-Quelle
OpenVoxCompiler    →  N pro Umgebung/Tier/Team
OpenVoxPuppetDB    →  Zukunft
```

Vorteile:
- **Mehrere Compiler-Pools** gegen eine CA (z.B. "production" und "ci")
- **Mehrere Code-Quellen** (verschiedene Control-Repos)
- **Unabhängige Lifecycle** — r10k CronJob läuft unabhängig vom Compiler-Rollout
- **Lose Kopplung** via `caRef` / `codeRef` Referenzen
- **Erweiterbar** — PuppetDB-CRD später ohne Breaking Change

### Referenz-Auflösung

Der Compiler-Controller schaut bei Reconciliation in die referenzierten CRs:

```
OpenVoxCompiler.spec.caRef.name: "production-ca"
  → OpenVoxCA "production-ca"
    → status.secretName: "production-ca-certs"   (CA Secret zum Mounten)
    → status.serviceName: "production-ca"         (für ssl bootstrap)

OpenVoxCompiler.spec.codeRef.name: "production-code"
  → OpenVoxR10k "production-code"
    → status.pvcName: "production-code"           (Code-PVC zum Mounten)
```

## Beispiel: Vollständiges Setup

```yaml
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: OpenVoxCA
metadata:
  name: main-ca
  namespace: openvox
spec:
  image: { repository: ghcr.io/slauger/openvoxserver, tag: "8.12.1" }
  certname: puppet
  dnsAltNames: [puppet, puppet-ca]
  autosign: "true"
  storage: { size: 1Gi }
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: OpenVoxR10k
metadata:
  name: control-repo
  namespace: openvox
spec:
  image: { repository: ghcr.io/slauger/r10k, tag: "latest" }
  sources:
    - name: puppet
      remote: https://github.com/example/control-repo.git
      basedir: /etc/puppetlabs/code/environments
  schedule: "*/5 * * * *"
  volume: { size: 5Gi }
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: OpenVoxCompiler
metadata:
  name: production
  namespace: openvox
spec:
  image: { repository: ghcr.io/slauger/openvoxserver, tag: "8.12.1" }
  caRef: { name: main-ca }
  codeRef: { name: control-repo }
  puppetdb:
    serverUrls: ["https://openvoxdb:8081"]
  replicas: 3
  maxActiveInstances: 2
  javaArgs: "-Xms512m -Xmx1024m"
```
