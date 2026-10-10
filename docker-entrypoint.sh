#!/bin/sh
set -e

# The relay runs as the unprivileged "haven" user. Started as root, this hands
# it the data folders first: they may be new volumes, or hold files written by
# an image that ran as root. Only files with another owner are changed.
if [ "$(id -u)" = "0" ]; then
  blossom="${BLOSSOM_PATH:-blossom}"
  mkdir -p db "$blossom"
  find db "$blossom" ! -user haven -exec chown haven:haven {} +
  chown haven:haven .
  exec setpriv --reuid=haven --regid=haven --init-groups "$@"
fi

exec "$@"
