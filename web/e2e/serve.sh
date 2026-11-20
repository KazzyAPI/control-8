#!/bin/sh
# Starts ../bin/control8 on :8123 with a fresh DB and break-glass password "e2e-password".
set -e
D=$(mktemp -d)
export C8_LISTEN=127.0.0.1:8123 C8_DATA_DIR=$D C8_PUBLIC_URL=http://127.0.0.1:8123
export C8_BREAKGLASS_HASH=$(printf 'e2e-password\n' | ../bin/control8 hash-password)
exec ../bin/control8
