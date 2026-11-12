#!/bin/sh
VERSION=1.0.0
# print it when run, not when sourced; `make bump-version` changes it
case "${0##*/}" in version.sh) echo "$VERSION" ;; esac
