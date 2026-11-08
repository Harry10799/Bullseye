#!/bin/sh
set -eu

node /app/dashboard-runtime-config.cjs
exec node /app/server.js
