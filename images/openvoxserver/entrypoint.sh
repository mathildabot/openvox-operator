#!/bin/bash
# Rootless entrypoint for OpenVox Server.
# Starts puppetserver directly via java — no ezbake, no runuser, no sudo.

set -o errexit
set -o pipefail
set -o nounset

# Source puppetserver configuration
. /etc/default/puppetserver

# Run initialization scripts
for f in /entrypoint.d/*.sh; do
    if [ -f "$f" ] && [ -x "$f" ]; then
        echo "Running $f"
        "$f"
    fi
done

echo "Starting OpenVox Server (direct java, PID $$)"

# Start puppetserver directly — the core from ezbake's foreground script,
# without the user-switching and PID file overhead.
exec ${JAVA_BIN} ${JAVA_ARGS} \
    --add-opens java.base/sun.nio.ch=ALL-UNNAMED \
    --add-opens java.base/java.io=ALL-UNNAMED \
    -Dlogappender=STDOUT \
    -cp "${INSTALL_DIR}/puppet-server-release.jar" \
    clojure.main -m puppetlabs.trapperkeeper.main \
    --config "${CONFIG}" \
    --bootstrap-config "${BOOTSTRAP_CONFIG}"
