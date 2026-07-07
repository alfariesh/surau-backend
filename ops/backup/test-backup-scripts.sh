#!/usr/bin/env bash
# Self-contained tests for the backup scripts: encryption round-trip and
# latest-archive selection. Needs age + zstd; no Docker, no R2, no Postgres.
set -Eeuo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
workdir="$(mktemp -d "${TMPDIR:-/tmp}/surau-backup-test.XXXXXX")"
trap 'rm -rf "$workdir"' EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$@"
  else
    shasum -a 256 "$@"
  fi
}

command -v age >/dev/null 2>&1 || fail "age is required for this test"
command -v zstd >/dev/null 2>&1 || fail "zstd is required for this test"

# --- 1. encryption round-trip: plaintext | zstd | age -> age -d | zstd -d ---
age-keygen -o "$workdir/age.key" 2>/dev/null
recipient="$(grep -o 'age1[a-z0-9]*' "$workdir/age.key" | head -n 1)"
[[ -n "$recipient" ]] || fail "could not derive recipient from generated key"

printf 'surau backup round-trip fixture %s\n' "$(date -u +%s)" >"$workdir/plain.txt"
zstd -q -c "$workdir/plain.txt" | age -e -r "$recipient" -o "$workdir/fixture.dump.zst.age"

# ciphertext must not contain the plaintext
if grep -q "round-trip fixture" "$workdir/fixture.dump.zst.age"; then
  fail "ciphertext leaks plaintext"
fi

(cd "$workdir" && sha256 fixture.dump.zst.age >fixture.dump.zst.age.sha256)
(cd "$workdir" && sha256 -c fixture.dump.zst.age.sha256 >/dev/null) || fail "checksum verification failed"

age -d -i "$workdir/age.key" "$workdir/fixture.dump.zst.age" | zstd -dc >"$workdir/roundtrip.txt"
cmp -s "$workdir/plain.txt" "$workdir/roundtrip.txt" || fail "decrypted content differs from original"

# decryption with a WRONG key must fail
age-keygen -o "$workdir/wrong.key" 2>/dev/null
if age -d -i "$workdir/wrong.key" "$workdir/fixture.dump.zst.age" >/dev/null 2>&1; then
  fail "decryption succeeded with the wrong key"
fi

echo "ok: encryption round-trip"

# --- 2. latest-archive selection must handle mixed .zst / .zst.age names ---
# Keep in sync with ARCHIVE_PATTERN in surau-pg-restore-check (guarded below).
pattern='^surau-postgres-[0-9TZ]+-[A-Za-z0-9._-]+\.dump\.zst(\.age)?$'
grep -qF "$pattern" "$here/surau-pg-restore-check" \
  || fail "ARCHIVE_PATTERN in surau-pg-restore-check drifted from the tested pattern"

listing="$(printf '%s\n' \
  'surau-postgres-20260701T040000Z-aaaaaaa.dump.zst' \
  'surau-postgres-20260701T040000Z-aaaaaaa.dump.zst.sha256' \
  'surau-postgres-20260707T040000Z-ccccccc.dump.zst.age' \
  'surau-postgres-20260707T040000Z-ccccccc.dump.zst.age.sha256' \
  'surau-postgres-20260706T040000Z-bbbbbbb.dump.zst' \
  'unrelated-file.txt')"

latest="$(printf '%s\n' "$listing" | grep -E "$pattern" | sort | tail -n 1)"
[[ "$latest" == "surau-postgres-20260707T040000Z-ccccccc.dump.zst.age" ]] \
  || fail "latest selection picked '$latest'"

# local selection (find-based) must also see both suffixes
mkdir -p "$workdir/backups"
touch "$workdir/backups/surau-postgres-20260706T040000Z-bbbbbbb.dump.zst"
touch "$workdir/backups/surau-postgres-20260707T040000Z-ccccccc.dump.zst.age"
local_latest="$(find "$workdir/backups" -maxdepth 1 -type f \( -name 'surau-postgres-*.dump.zst' -o -name 'surau-postgres-*.dump.zst.age' \) -print | sort | tail -n 1)"
[[ "$(basename "$local_latest")" == "surau-postgres-20260707T040000Z-ccccccc.dump.zst.age" ]] \
  || fail "local latest selection picked '$local_latest'"

echo "ok: latest-archive selection"
echo "all backup script tests passed"
