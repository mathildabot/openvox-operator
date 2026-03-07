#!/bin/sh

set -e

echo "System configuration values:"
echo "* HOSTNAME: '${HOSTNAME}'"
echo "* hostname -f: '$(hostname -f 2>/dev/null || echo unknown)'"

ssl_dir=$(puppet config print ssldir)

if [ -n "${CERTNAME}" ]; then
  echo "* CERTNAME: '${CERTNAME}'"
  certname=${CERTNAME}.pem
else
  echo "* CERTNAME: unset"
  if [ ! -d "${ssl_dir}/certs" ]; then
    certname="Not-Found"
  else
    certname=$(cd "${ssl_dir}/certs" && find * -type f -name '*.pem' ! -name ca.pem -print0 2>/dev/null | xargs -0 ls -1tr 2>/dev/null | head -n 1)
    if [ -z "${certname}" ]; then
      echo "WARNING: No certificates found in ${ssl_dir}/certs"
    fi
  fi
fi

echo "* OPENVOXSERVER_PORT: '${OPENVOXSERVER_PORT:-8140}'"
echo "* Certname: '${certname}'"
echo "* DNS_ALT_NAMES: '${DNS_ALT_NAMES}'"
echo "* SSLDIR: '${ssl_dir}'"

altnames="-certopt no_subject,no_header,no_version,no_serial,no_signame,no_validity,no_issuer,no_pubkey,no_sigdump,no_aux"

if [ -f "${ssl_dir}/certs/ca.pem" ]; then
  echo "CA Certificate:"
  openssl x509 -subject -issuer -text -noout -in "${ssl_dir}/certs/ca.pem" $altnames
fi

if [ -n "${certname}" ] && [ -f "${ssl_dir}/certs/${certname}" ]; then
  echo "Certificate ${certname}:"
  openssl x509 -subject -issuer -text -noout -in "${ssl_dir}/certs/${certname}" $altnames
fi
