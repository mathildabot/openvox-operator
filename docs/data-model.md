# Datenmodell — CRD Design

## API Group

`openvox.voxpupuli.org/v1alpha1`

## Übersicht

| Kind | Verantwortung | Erzeugt K8s-Ressourcen |
|---|---|---|
| `Environment` | Shared Config, CA-Lifecycle, PuppetDB-Verbindung | ConfigMaps, CA Job, CA Secret, CA PVC, CA Service |
| `Pool` | Besitzt einen K8s Service | Service |
| `Server` | Puppetserver Deployment/StatefulSet | Deployment oder StatefulSet, HPA |
| `CodeDeploy` | r10k Code-Deployment aus Git | PVC, Job, CronJob |
| `Database` | OpenVoxDB *(Zukunft)* | StatefulSet, Service |

## Beziehungen

```
Environment ◄── Server (environmentRef)
Environment ◄── Pool (environmentRef)
Environment ◄── CodeDeploy (environmentRef)
Pool        ◄── Server (poolRef)
```

Mehrere Server können dasselbe Environment und denselben Pool referenzieren.
Ein Environment kann von beliebig vielen Servern, Pools und CodeDeploys referenziert werden.

---

## 1. `Environment`

Die Klammer für ein Puppet-Setup. Verwaltet die CA, shared Konfiguration und PuppetDB-Verbindung.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Environment
metadata:
  name: production
spec:
  # Default-Image für alle Server in diesem Environment (überschreibbar pro Server)
  image:
    repository: ghcr.io/slauger/openvoxserver
    tag: "8.12.1"

  # CA-Konfiguration
  ca:
    certname: puppet
    dnsAltNames:
      - puppet
      - puppet-ca
      - puppet-ca.openvox.svc
    ttl: 157680000                     # 5 Jahre
    allowSubjectAltNames: true
    autosign: "true"                   # true/false/script-path
    storage:
      size: 1Gi
      storageClass: ""                 # leer = default StorageClass
    # Optional: Intermediate CA
    # intermediateCA:
    #   secretName: my-intermediate-ca

  # PuppetDB-Verbindung (extern bereitgestellt)
  puppetdb:
    serverUrls:
      - https://openvoxdb:8081

  # Shared puppet.conf Einstellungen
  puppet:
    environmentTimeout: unlimited
    environmentPath: /etc/puppetlabs/code/environments
    hieraConfig: "$confdir/hiera.yaml"
    storeconfigs: true
    storeBackend: puppetdb
    reports: puppetdb
    extraConfig: {}                    # beliebige key=value Paare

status:
  phase: Running                       # Pending | CASetup | Running | Error
  caReady: true
  caSecretName: production-ca-certs    # auto-generiert
  caServiceName: production-ca         # auto-generiert (interner ClusterIP)
  conditions:
    - type: CAInitialized
      status: "True"
    - type: ConfigReady
      status: "True"
```

**Erzeugt**:
- CA Setup Job (einmalig, `puppetserver ca setup`)
- CA Secret (`<name>-ca-certs`: ca_crt.pem, ca_key.pem, ca_crl.pem)
- CA PVC (`<name>-ca-data`)
- CA Service (`<name>-ca`, interner ClusterIP, immer — braucht keinen Pool)
- ConfigMaps (`<name>-puppet-conf`, `<name>-puppetdb-conf`, `<name>-webserver-conf`, etc.)

---

## 2. `Pool`

Besitzt einen Kubernetes Service. Löst das Ownership-Problem wenn mehrere Server denselben Service teilen.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
spec:
  environmentRef: production

  service:
    type: LoadBalancer               # ClusterIP | LoadBalancer | NodePort
    port: 8140
    annotations:
      service.beta.kubernetes.io/aws-load-balancer-type: nlb
    labels:
      team: platform

status:
  serviceName: puppet                # der erstellte K8s Service
  endpoints: 4                       # Anzahl Pods hinter dem Service
```

**Erzeugt**:
- Kubernetes Service (`<name>:8140`)
- Der Service selektiert alle Server-Pods die diesen Pool referenzieren via Label `openvox.voxpupuli.org/pool: <name>`

**Lifecycle**: Löscht man einen Server, bleibt der Service. Löscht man den Pool, ist der Service weg.

---

## 3. `Server`

Ein Pool von Puppetserver-Instanzen. Kann als CA, Compiler, oder beides laufen.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: stable
spec:
  environmentRef: production         # Pflicht: gehört zu diesem Environment
  poolRef: puppet                    # Optional: tritt diesem Pool/Service bei

  # Image-Override (sonst Environment-Default)
  image:
    tag: "8.12.1"

  # CA-Rolle
  ca:
    enabled: false                   # true = CA-Service aktiviert, mountet CA-Daten
    compiler: false                  # true = CA nimmt auch am Pool-Service teil

  replicas: 3
  autoscaling:
    enabled: false
    minReplicas: 1
    maxReplicas: 10
    targetCPU: 75

  resources:
    requests: { memory: "1Gi", cpu: "500m" }
    limits:   { memory: "2Gi" }
  javaArgs: "-Xms512m -Xmx1024m"
  maxActiveInstances: 2              # JRuby-Instanzen pro Pod

  # Optional: zusätzliche DNS-Alt-Names für das Server-Zertifikat
  dnsAltNames:
    - puppet
    - puppet.openvox.svc

status:
  phase: Running                     # Pending | WaitingForCA | Running | Error
  ready: 3
  desired: 3
  conditions:
    - type: SSLBootstrapped
      status: "True"
    - type: Ready
      status: "True"
```

**Erzeugt**:
- Deployment (wenn `ca.enabled: false`) oder StatefulSet (wenn `ca.enabled: true`)
- HPA (wenn `autoscaling.enabled`)
- InitContainer für SSL-Bootstrap gegen CA-Service (wenn `ca.enabled: false`)

**Labels auf den Pods**:
- `openvox.voxpupuli.org/environment: production`
- `openvox.voxpupuli.org/pool: puppet` (wenn poolRef gesetzt)
- `openvox.voxpupuli.org/server: stable`
- `openvox.voxpupuli.org/role: compiler` oder `ca`

**CA-Server + Compiler Combo** (kleine Setups):
```yaml
kind: Server
metadata:
  name: puppet
spec:
  environmentRef: lab
  poolRef: puppet
  ca:
    enabled: true
    compiler: true      # Pods bekommen AUCH das Pool-Label → landen im Service
  replicas: 1
```

---

## 4. `CodeDeploy`

r10k Code-Deployment aus Git. Unabhängig von Server — verwaltet ein PVC das von Servern gemountet wird.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: CodeDeploy
metadata:
  name: control-repo
spec:
  environmentRef: production

  image:
    repository: ghcr.io/slauger/r10k
    tag: "latest"

  sources:
    - name: puppet
      remote: https://github.com/example/control-repo.git
      basedir: /etc/puppetlabs/code/environments

  # Git-Authentifizierung (optional)
  # gitSecret: r10k-git-credentials

  schedule: "*/5 * * * *"           # CronJob für periodisches Update

  volume:
    size: 5Gi
    accessMode: ReadWriteOnce        # ReadWriteOnce oder ReadWriteMany
    # storageClass: ""
    # existingClaim: ""              # vorhandenes PVC nutzen

status:
  phase: Ready                       # Pending | Deploying | Ready | Error
  lastDeployTime: "2026-03-07T22:00:00Z"
  pvcName: control-repo-code         # auto-generiert
  conditions:
    - type: VolumeReady
      status: "True"
    - type: LastDeploySucceeded
      status: "True"
```

**Erzeugt**:
- PVC (`<name>-code`)
- Job (initial deploy)
- CronJob (periodischer Sync nach `schedule`)

### Code Volume Strategie

Server mounten das Code-PVC automatisch wenn ein `CodeDeploy` im gleichen Environment existiert.

**Single-Node (Default): RWO + Pod-Affinity**

RWO erlaubt mehrere Pods auf **demselben Node**. Der Operator setzt Pod-Affinity damit r10k Job und Server-Pods auf dem gleichen Node laufen.

```
Node A:
  ├── r10k CronJob     ─── mount RWO PVC (read-write)
  ├── Server Pod 1      ── mount RWO PVC (read-only)
  └── Server Pod 2      ── mount RWO PVC (read-only)
```

**Multi-Node: RWX**

Sobald Server über mehrere Nodes verteilt werden, braucht man `accessMode: ReadWriteMany`. Benötigt einen RWX-fähigen Storage-Provider (NFS, CephFS, EFS, Longhorn).

| Setup | accessMode | Voraussetzung |
|---|---|---|
| Single-Node | `ReadWriteOnce` | Jeder Storage-Provider |
| Multi-Node | `ReadWriteMany` | NFS, CephFS, EFS, Longhorn |

---

## 5. `Database` *(Zukunft)*

Für den Fall dass man OpenVoxDB auch vom Operator managen lassen will.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Database
metadata:
  name: openvoxdb
spec:
  environmentRef: production
  image:
    repository: ghcr.io/slauger/openvoxdb
    tag: "8.12.1"
  postgresql:
    host: postgres.db.svc
    port: 5432
    database: puppetdb
    credentialsSecret: puppetdb-postgres-credentials
  storage:
    size: 10Gi
```

Erstmal **out of scope** — PuppetDB und PostgreSQL kommen extern über `Environment.spec.puppetdb`. Das Datenmodell ist aber vorbereitet.

---

## Referenz-Auflösung

Der Server-Controller löst bei jeder Reconciliation die Referenzen auf:

```
Server.spec.environmentRef: "production"
  → Environment "production"
    → status.caSecretName: "production-ca-certs"    (CA Secret zum Mounten)
    → status.caServiceName: "production-ca"          (für SSL Bootstrap)
    → ConfigMaps: "production-puppet-conf", etc.     (zum Mounten)

Server.spec.poolRef: "puppet"
  → Pool "puppet"
    → Label "openvox.voxpupuli.org/pool: puppet"     (für Service-Selektion)

CodeDeploy im gleichen Environment:
  → CodeDeploy "control-repo"
    → status.pvcName: "control-repo-code"            (Code-PVC zum Mounten)
```

---

## Vollständiges Beispiel

```yaml
---
# 1. Environment: Shared Config + CA
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Environment
metadata:
  name: production
  namespace: openvox
spec:
  image: { repository: ghcr.io/slauger/openvoxserver, tag: "8.12.1" }
  ca:
    certname: puppet
    dnsAltNames: [puppet, puppet-ca]
    autosign: "true"
    storage: { size: 1Gi }
  puppetdb:
    serverUrls: ["https://openvoxdb:8081"]
  puppet:
    environmentTimeout: unlimited
    storeconfigs: true
    reports: puppetdb
---
# 2. Pool: Besitzt den Service für Agents
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
  namespace: openvox
spec:
  environmentRef: production
  service:
    type: LoadBalancer
    port: 8140
---
# 3. Server: CA (1 Replica, kein Pool)
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: ca
  namespace: openvox
spec:
  environmentRef: production
  ca: { enabled: true }
  replicas: 1
  javaArgs: "-Xms512m -Xmx1024m"
---
# 4. Server: Stable Compiler Pool
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: stable
  namespace: openvox
spec:
  environmentRef: production
  poolRef: puppet
  replicas: 3
  maxActiveInstances: 2
  javaArgs: "-Xms512m -Xmx1024m"
---
# 5. Server: Canary (neue Version, gleicher Pool)
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: canary
  namespace: openvox
spec:
  environmentRef: production
  poolRef: puppet
  image: { tag: "8.13.0" }
  replicas: 1
  javaArgs: "-Xms512m -Xmx1024m"
---
# 6. CodeDeploy: r10k
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: CodeDeploy
metadata:
  name: control-repo
  namespace: openvox
spec:
  environmentRef: production
  image: { repository: ghcr.io/slauger/r10k, tag: "latest" }
  sources:
    - name: puppet
      remote: https://github.com/example/control-repo.git
      basedir: /etc/puppetlabs/code/environments
  schedule: "*/5 * * * *"
  volume: { size: 5Gi }
```

### kubectl Output

```
$ kubectl -n openvox get environment,pool,server,codedeploy

NAME                                          CA READY   AGE
environment.openvox.voxpupuli.org/production  true       1h

NAME                                 TYPE           AGE
pool.openvox.voxpupuli.org/puppet    LoadBalancer   1h

NAME                                  ENVIRONMENT   POOL     IMAGE    REPLICAS   READY
server.openvox.voxpupuli.org/ca       production             8.12.1   1          1
server.openvox.voxpupuli.org/stable   production    puppet   8.12.1   3          3
server.openvox.voxpupuli.org/canary   production    puppet   8.13.0   1          1

NAME                                            ENVIRONMENT   SCHEDULE      LAST DEPLOY
codedeploy.openvox.voxpupuli.org/control-repo   production    */5 * * * *   2m ago
```
