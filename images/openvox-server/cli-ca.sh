#!/usr/bin/env bash
# JRuby-compatible wrapper for `puppetserver ca` subcommand.
# The upstream ca CLI script uses a CRuby shebang which is not
# available in the rootless runtime image. This wrapper runs the
# same Ruby code via the puppetserver JRuby CLI instead.

umask 0022

cli_defaults=${INSTALL_DIR}/cli/cli-defaults.sh
CLASSPATH=${INSTALL_DIR}/puppet-server-release.jar

if [ -e "$cli_defaults" ]; then
  # shellcheck disable=SC1090
  . "$cli_defaults"
  if [ $? -ne 0 ]; then
    echo "Unable to initialize cli defaults, failing ca subcommand." 1>&2
    exit 1
  fi
fi

# shellcheck disable=SC2086
"${JAVA_BIN}" $JAVA_ARGS_CLI \
    -cp "$CLASSPATH" \
    clojure.main -m puppetlabs.puppetserver.cli.ruby \
    --config "${CONFIG}" -- \
    -e "require %(puppetserver/ca/cli); exit Puppetserver::Ca::Cli.run(ARGV)" -- "$@"
