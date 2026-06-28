#!/bin/sh
set -eu

mkdir -p /app/data

if [ "$(id -u)" = "0" ]; then
	if ! chown -R app:app /app/data; then
		echo "WARN: failed to chown /app/data; continuing as app, SQLite startup may fail if the directory is not writable" >&2
	fi
	exec su-exec app "$@"
fi

exec "$@"
