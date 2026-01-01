#!/bin/sh
set -e

# Stop the service if running
if [ -d /run/systemd/system ]; then
    systemctl stop go-cluster >/dev/null 2>&1 || true
    systemctl disable go-cluster >/dev/null 2>&1 || true
fi

echo "go-cluster service stopped and disabled."
