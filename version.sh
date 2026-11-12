#!/bin/sh
VERSION=0.1.0
# print it when run (not when sourced, by scripts/bump-version.sh say)
case "${0##*/}" in version.sh) echo "$VERSION" ;; esac
