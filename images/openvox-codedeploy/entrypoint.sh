#!/bin/bash
set -e

# OpenShift random-UID support: inject current UID into /etc/passwd
# so that SSH and Git can resolve the username.
if ! whoami &>/dev/null; then
    echo "codedeploy:x:$(id -u):0::/home/codedeploy:/bin/bash" >> /etc/passwd
fi

exec r10k "$@"
