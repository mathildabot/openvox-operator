#!/bin/bash

set -e

if test -n "${AUTOSIGN}" ; then
  puppet config set autosign "$AUTOSIGN" --section server
fi
