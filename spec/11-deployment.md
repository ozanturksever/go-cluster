# Deployment

## Installation

### One-liner Install

```bash
curl -fsSL https://get.go-cluster.dev | bash

# Or with specific version
curl -fsSL https://get.go-cluster.dev | bash -s -- v1.0.0
```

### Package Managers

```bash
# Homebrew (macOS/Linux)
brew install ozanturksever/tap/go-cluster

# APT (Debian/Ubuntu)
curl -fsSL https://apt.go-cluster.dev/gpg | sudo gpg --dearmor -o /usr/share/keyrings/go-cluster.gpg
echo "deb [signed-by=/usr/share/keyrings/go-cluster.gpg] https://apt.go-cluster.dev stable main" | sudo tee /etc/apt/sources.list.d/go-cluster.list
sudo apt update && sudo apt install go-cluster

# YUM/DNF (RHEL/Fedora)
sudo dnf config-manager --add-repo https://rpm.go-cluster.dev/go-cluster.repo
sudo dnf install go-cluster
```

---

## Systemd Integration

```ini
# /lib/systemd/system/go-cluster@.service
[Unit]
Description=go-cluster node for %i
After=network-online.target nats.service
Requires=nats.service

[Service]
Type=notify
User=go-cluster
Group=go-cluster

EnvironmentFile=/etc/go-cluster/%i.env
ExecStart=/usr/bin/go-cluster run \
    --cluster %i \
    --node ${NODE_ID} \
    --nats ${NATS_URL}

Restart=on-failure
RestartSec=5
WatchdogSec=30

NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/go-cluster/%i

[Install]
WantedBy=multi-user.target
```

```bash
# /etc/go-cluster/myapp.env
CLUSTER_ID=myapp
NODE_ID=node-1
NATS_URL=nats://localhost:4222
```

**Usage:**
```bash
sudo systemctl enable --now go-cluster@myapp
sudo systemctl status go-cluster@myapp
journalctl -u go-cluster@myapp -f
```

---

## Docker

```dockerfile
FROM alpine:3.19 AS base
RUN apk add --no-cache ca-certificates tzdata

FROM scratch
COPY --from=base /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY go-cluster /usr/bin/go-cluster

EXPOSE 8080 9090
VOLUME ["/data"]

ENTRYPOINT ["/usr/bin/go-cluster"]
CMD ["run"]
```

```yaml
# docker-compose.yml
version: "3.8"

services:
  nats:
    image: nats:2.10
    command: --jetstream --store_dir /data
    ports:
      - "4222:4222"

  app-node-1:
    image: ghcr.io/ozanturksever/go-cluster:latest
    environment:
      - CLUSTER_ID=myapp
      - NODE_ID=node-1
      - NATS_URL=nats://nats:4222
      - DB_PATH=/data/app.db
    volumes:
      - node1-data:/data
    cap_add:
      - NET_ADMIN
```

---

## Kubernetes

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: myapp
spec:
  serviceName: myapp
  replicas: 3
  selector:
    matchLabels:
      app: myapp
  template:
    spec:
      containers:
        - name: myapp
          image: ghcr.io/ozanturksever/go-cluster:latest
          env:
            - name: CLUSTER_ID
              value: myapp
            - name: NODE_ID
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
            - name: NATS_URL
              valueFrom:
                configMapKeyRef:
                  name: myapp-config
                  key: nats-url
          ports:
            - containerPort: 8080
              name: health
            - containerPort: 9090
              name: metrics
          livenessProbe:
            httpGet:
              path: /health
              port: health
          readinessProbe:
            httpGet:
              path: /ready
              port: health
          volumeMounts:
            - name: data
              mountPath: /data
  volumeClaimTemplates:
    - metadata:
        name: data
      spec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 10Gi
```

---

## CLI Commands

```bash
go-cluster <command> [flags]

# Node group commands
  node list                         List nodes in namespace
  node status <node-id>             Show node status
  node drain <node-id>              Drain node
  
# Cluster commands
  run                               Run node
  status                            Show cluster status
  stepdown                          Gracefully step down as leader
  health                            Check health
  
# Data
  snapshot create                   Create snapshot
  snapshot list                     List snapshots
  snapshot restore <id>             Restore from snapshot
  
# Upgrades
  upgrade                           Rolling upgrade
  canary                            Canary deployment

Flags:
  --namespace, -n   Namespace
  --cluster, -c     Cluster ID
  --node            Node ID
  --nats            NATS URL
```

---

## Upgrade Patterns

### Rolling Upgrade

```bash
go-cluster upgrade --cluster myapp --version v1.1.0

# Or one node at a time
go-cluster upgrade --cluster myapp --node node-3 --version v1.1.0
go-cluster upgrade --cluster myapp --node node-2 --version v1.1.0
go-cluster upgrade --cluster myapp --node node-1 --version v1.1.0
```

### Blue-Green Deployment

```bash
go-cluster deploy blue-green \
    --cluster myapp \
    --new-version v2.0.0

go-cluster switch --from myapp-blue --to myapp-green
go-cluster destroy --cluster myapp-blue
```

### Canary Deployment

```bash
go-cluster deploy canary \
    --cluster myapp \
    --new-version v1.1.0 \
    --canary-percentage 10

go-cluster canary promote --cluster myapp
# or
go-cluster canary rollback --cluster myapp
```
