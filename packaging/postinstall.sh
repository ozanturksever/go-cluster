#!/bin/sh
set -e

# Create go-cluster user and group if they don't exist
if ! getent group go-cluster >/dev/null 2>&1; then
    groupadd --system go-cluster
fi

if ! getent passwd go-cluster >/dev/null 2>&1; then
    useradd --system --gid go-cluster --shell /sbin/nologin \
        --home-dir /var/lib/go-cluster --create-home \
        --comment "go-cluster daemon" go-cluster
fi

# Create data directory
mkdir -p /var/lib/go-cluster
chown go-cluster:go-cluster /var/lib/go-cluster
chmod 750 /var/lib/go-cluster

# Reload systemd if available
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload >/dev/null 2>&1 || true
fi

echo "go-cluster installed successfully!"
echo ""
echo "To start the service:"
echo "  sudo systemctl enable go-cluster"
echo "  sudo systemctl start go-cluster"
echo ""
echo "Configuration file: /etc/go-cluster/go-cluster.yaml"
