#!/usr/bin/env bash
set -euo pipefail

# DEV-only A-2 rollout helper. It moves collab from the existing owner DSN to
# one narrow LOGIN role without putting generated passwords in argv or logs.
# The production runbook remains interactive: docs/service-identity-rotation.md.

MODE="${1:-}"
ENV_FILE="${ENV_FILE:-.env.production}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.prod.yml}"
ROLE_NAME="surau_collab_dev_202607_b"
GROUP_ROLE="surau_collab_store"
ACTIVE_CONTAINER_FILE="${ACTIVE_CONTAINER_FILE:-/var/lib/surau/deploy/active-api-container}"

if [[ "$MODE" != "prepare" && "$MODE" != "cutover" ]]; then
  echo "usage: $0 prepare|cutover" >&2
  exit 2
fi

env_value() {
  local key="$1"
  sed -n "s/^${key}=//p" "$ENV_FILE" | tail -1
}

set_env_value() {
  local key="$1"
  local value="$2"
  local temporary
  temporary="$(mktemp "${ENV_FILE}.tmp.XXXXXX")"
  awk -v key="$key" -v value="$value" '
    BEGIN { found = 0 }
    index($0, key "=") == 1 { print key "=" value; found = 1; next }
    { print }
    END { if (!found) print key "=" value }
  ' "$ENV_FILE" > "$temporary"
  chmod 0600 "$temporary"
  mv -f "$temporary" "$ENV_FILE"
}

app_container_id() {
  local container_id
  container_id="$(sudo cat "$ACTIVE_CONTAINER_FILE" 2>/dev/null || true)"
  if [[ "$container_id" =~ ^[a-f0-9]{12,64}$ ]] &&
     [[ "$(sudo docker inspect -f '{{.State.Running}}' "$container_id" 2>/dev/null || true)" == true ]]; then
    printf '%s\n' "$container_id"

    return
  fi
  compose ps -q app | head -1
}

SECRETS_DIR="$(env_value COLLAB_SECRETS_DIR)"
SECRETS_DIR="${SECRETS_DIR:-/var/lib/surau/secrets/collab}"
if [[ "$SECRETS_DIR" != /* ]]; then
  echo "COLLAB_SECRETS_DIR must be absolute" >&2
  exit 1
fi

PG_URL_FILE="$SECRETS_DIR/pg-url"
TOKEN_FILE="$SECRETS_DIR/service-token"

SECRETS_GID="$(env_value COLLAB_SECRETS_GID)"
if [[ -z "$SECRETS_GID" ]]; then
  if ! getent group surau-collab-secrets >/dev/null; then
    sudo groupadd --system surau-collab-secrets
  fi
  SECRETS_GID="$(getent group surau-collab-secrets | awk -F: '{print $3}')"
  set_env_value COLLAB_SECRETS_GID "$SECRETS_GID"
fi
if [[ ! "$SECRETS_GID" =~ ^[0-9]+$ ]]; then
  echo "COLLAB_SECRETS_GID must be numeric" >&2
  exit 1
fi

# The host owns the files as root. Only the collab container's supplementary
# numeric group can read them; the Node process remains non-root.
sudo install -d -o root -g "$SECRETS_GID" -m 0750 "$SECRETS_DIR"

atomic_write_root() {
  local target="$1"
  local value="$2"
  local temporary
  temporary="$(sudo mktemp "${target}.tmp.XXXXXX")"
  if ! printf '%s\n' "$value" | sudo tee "$temporary" >/dev/null; then
    sudo rm -f "$temporary"
    return 1
  fi
  sudo chown "root:$SECRETS_GID" "$temporary"
  sudo chmod 0640 "$temporary"
  sudo mv -f "$temporary" "$target"
}

prepare_overlap() {
  local token owner_url
  if ! sudo test -s "$TOKEN_FILE"; then
    token="$(env_value COLLAB_SERVICE_TOKEN)"
    if (( ${#token} < 32 )); then
      echo "COLLAB_SERVICE_TOKEN is required to bootstrap the collab T1 file" >&2
      exit 1
    fi
    atomic_write_root "$TOKEN_FILE" "$token"
  fi

  if ! sudo test -s "$PG_URL_FILE"; then
    owner_url="$(env_value PG_URL)"
    if [[ -z "$owner_url" ]]; then
      echo "PG_URL is required for the one-release collab A overlap" >&2
      exit 1
    fi
    atomic_write_root "$PG_URL_FILE" "$owner_url"
    set_env_value ALLOW_LEGACY_DB_CREDENTIALS true
    echo "collab A overlap prepared; owner fallback is explicitly enabled"
  else
    echo "collab credential files already exist"
  fi

  sudo chown "root:$SECRETS_GID" "$PG_URL_FILE" "$TOKEN_FILE"
  sudo chmod 0640 "$PG_URL_FILE" "$TOKEN_FILE"
}

compose() {
  sudo docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

db_psql() {
  # Expansion is intentionally deferred to the database container.
  # shellcheck disable=SC2016
  compose exec -T db sh -c \
    'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" "$@"' sh "$@"
}

assert_role_acl() {
  local acl_ok
  acl_ok="$(db_psql -Atc "
    SELECT
      NOT r.rolsuper
      AND NOT r.rolcreatedb
      AND NOT r.rolcreaterole
      AND NOT r.rolreplication
      AND NOT r.rolbypassrls
      AND r.rolcanlogin
      AND r.rolvaliduntil > now()
      AND r.rolvaliduntil <= now() + interval '90 days'
      AND (SELECT count(*) FROM pg_auth_members m WHERE m.member = r.oid) = 1
      AND EXISTS (
        SELECT 1
        FROM pg_auth_members m
        JOIN pg_roles parent ON parent.oid = m.roleid
        WHERE m.member = r.oid
          AND parent.rolname = '$GROUP_ROLE'
          AND NOT m.admin_option
      )
      AND has_table_privilege('$ROLE_NAME', 'public.collab_documents', 'SELECT')
      AND has_column_privilege('$ROLE_NAME', 'public.collab_documents', 'name', 'INSERT')
      AND has_column_privilege('$ROLE_NAME', 'public.collab_documents', 'state', 'UPDATE')
      AND NOT has_table_privilege('$ROLE_NAME', 'public.collab_documents', 'DELETE')
      AND NOT has_table_privilege('$ROLE_NAME', 'public.book_pages', 'SELECT')
      AND NOT has_table_privilege('$ROLE_NAME', 'public.users', 'SELECT')
    FROM pg_roles r
    WHERE r.rolname = '$ROLE_NAME';")"
  if [[ "$acl_ok" != "t" ]]; then
    echo "collab DEV role ACL/expiry/membership drill failed" >&2
    exit 1
  fi
}

assert_dev_evidence() {
  local audit_ok activity_count
  assert_role_acl

  activity_count="$(db_psql -Atc \
    "SELECT count(*) FROM pg_stat_activity WHERE usename = '$ROLE_NAME';")"
  if (( activity_count < 1 )); then
    echo "collab did not activate the dedicated DB role" >&2
    exit 1
  fi

  audit_ok="$(db_psql -Atc "
    SELECT EXISTS (
      SELECT 1
      FROM service_request_audit_logs
      WHERE principal_name = 'collab-server'
        AND route_template = '/internal/collab/whoami'
        AND auth_outcome = 'allowed'
        AND response_status = 200
        AND started_at > now() - interval '15 minutes'
    );")"
  if [[ "$audit_ok" != "t" ]]; then
    echo "collab principal audit drill failed" >&2
    exit 1
  fi
}

cutover_role() {
  local current_url database_name password valid_until
  local db_container sql_host sql_container pgpass_host pgpass_container
  local candidate_host before_app before_collab after_app after_collab

  current_url="$(sudo sed -n '1p' "$PG_URL_FILE")"
  if [[ "$current_url" == "postgres://$ROLE_NAME:"* || \
        "$current_url" == "postgresql://$ROLE_NAME:"* ]]; then
    curl -fsS http://127.0.0.1:8090/healthz >/dev/null
    assert_dev_evidence
    set_env_value ALLOW_LEGACY_DB_CREDENTIALS false
    echo "collab DEV role was already cut over and remains valid"
    return
  fi

  database_name="$(env_value POSTGRES_DB)"
  if [[ ! "$database_name" =~ ^[A-Za-z0-9_-]+$ ]]; then
    echo "POSTGRES_DB contains characters unsafe for the generated DEV DSN" >&2
    exit 1
  fi
  password="$(openssl rand -hex 32)"
  valid_until="$(date -u -d '+89 days' '+%Y-%m-%dT%H:%M:%SZ')"
  db_container="$(compose ps -q db)"
  if [[ -z "$db_container" ]]; then
    echo "database container is unavailable" >&2
    exit 1
  fi

  sql_host="$(sudo mktemp "$SECRETS_DIR/.collab-role.XXXXXX.sql")"
  sql_container="/tmp/a2-collab-role-$$.sql"
  pgpass_host="$(sudo mktemp "$SECRETS_DIR/.collab-pgpass.XXXXXX")"
  pgpass_container="/tmp/a2-collab-pgpass-$$"
  candidate_host="$(sudo mktemp "$SECRETS_DIR/.pg-url-candidate.XXXXXX")"
  cleanup() {
    sudo rm -f "$sql_host" "$pgpass_host"
    if [[ -n "$candidate_host" ]]; then
      sudo rm -f "$candidate_host"
    fi
    sudo docker exec "$db_container" rm -f "$sql_container" "$pgpass_container" \
      >/dev/null 2>&1 || true
  }
  trap cleanup EXIT

  # The generated password exists only in root-owned temporary files and is
  # consumed over psql input/PGPASSFILE; it never appears in argv or logs.
  {
    printf '\\set ON_ERROR_STOP on\n'
    # $a$ is a literal PostgreSQL dollar-quote delimiter.
    # shellcheck disable=SC2016
    printf 'DO $a$\nBEGIN\n'
    printf "  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN\n" "$ROLE_NAME"
    printf "    EXECUTE 'CREATE ROLE %s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS';\n" "$ROLE_NAME"
    printf '  END IF;\n'
    # shellcheck disable=SC2016
    printf 'END\n$a$;\n'
    printf "ALTER ROLE %s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS VALID UNTIL '%s';\n" \
      "$ROLE_NAME" "$valid_until"
    printf "ALTER ROLE %s PASSWORD '%s';\n" "$ROLE_NAME" "$password"
    printf "GRANT %s TO %s;\n" "$GROUP_ROLE" "$ROLE_NAME"
  } | sudo tee "$sql_host" >/dev/null
  printf '127.0.0.1:5432:%s:%s:%s\n' "$database_name" "$ROLE_NAME" "$password" \
    | sudo tee "$pgpass_host" >/dev/null
  printf 'postgres://%s:%s@db:5432/%s?sslmode=disable\n' \
    "$ROLE_NAME" "$password" "$database_name" | sudo tee "$candidate_host" >/dev/null
  sudo chown root:root "$sql_host" "$pgpass_host" "$candidate_host"
  sudo chmod 0600 "$sql_host" "$pgpass_host" "$candidate_host"

  sudo docker cp "$sql_host" "$db_container:$sql_container"
  sudo docker cp "$pgpass_host" "$db_container:$pgpass_container"
  sudo docker exec "$db_container" chown postgres:postgres "$sql_container" "$pgpass_container"
  sudo docker exec "$db_container" chmod 0600 "$sql_container" "$pgpass_container"
  db_psql -f "$sql_container" >/dev/null
  assert_role_acl

  sudo docker exec -u postgres -e "PGPASSFILE=$pgpass_container" "$db_container" \
    psql -v ON_ERROR_STOP=1 -h 127.0.0.1 -U "$ROLE_NAME" -d "$database_name" \
    -c 'SELECT 1 FROM collab_documents LIMIT 1' >/dev/null
  if sudo docker exec -u postgres -e "PGPASSFILE=$pgpass_container" "$db_container" \
    psql -v ON_ERROR_STOP=1 -h 127.0.0.1 -U "$ROLE_NAME" -d "$database_name" \
    -c 'DELETE FROM collab_documents WHERE false' >/dev/null 2>&1; then
    echo "collab DEV role unexpectedly acquired DELETE" >&2
    exit 1
  fi
  if sudo docker exec -u postgres -e "PGPASSFILE=$pgpass_container" "$db_container" \
    psql -v ON_ERROR_STOP=1 -h 127.0.0.1 -U "$ROLE_NAME" -d "$database_name" \
    -c 'SELECT 1 FROM users LIMIT 1' >/dev/null 2>&1; then
    echo "collab DEV role unexpectedly acquired users access" >&2
    exit 1
  fi

  before_app="$(app_container_id | xargs sudo docker inspect -f '{{.Id}} {{.State.StartedAt}}')"
  before_collab="$(compose ps -q collab | xargs sudo docker inspect -f '{{.Id}} {{.State.StartedAt}}')"
  sudo mv -f "$candidate_host" "$PG_URL_FILE"
  candidate_host=""
  sudo chown "root:$SECRETS_GID" "$PG_URL_FILE"
  sudo chmod 0640 "$PG_URL_FILE"

  for _ in {1..20}; do
    if curl -fsS http://127.0.0.1:8090/healthz >/dev/null; then
      sleep 1
      break
    fi
    sleep 1
  done
  curl -fsS http://127.0.0.1:8090/healthz >/dev/null
  after_app="$(app_container_id | xargs sudo docker inspect -f '{{.Id}} {{.State.StartedAt}}')"
  after_collab="$(compose ps -q collab | xargs sudo docker inspect -f '{{.Id}} {{.State.StartedAt}}')"
  if [[ "$before_app" != "$after_app" || "$before_collab" != "$after_collab" ]]; then
    echo "collab DB cutover restarted a container" >&2
    exit 1
  fi

  assert_dev_evidence
  set_env_value ALLOW_LEGACY_DB_CREDENTIALS false
  # EXIT is the failure cleanup path while this function's locals are alive.
  # On success, clean explicitly and remove the trap before returning so Bash
  # cannot expand out-of-scope local paths at process exit.
  cleanup
  trap - EXIT
  echo "collab DEV DB role cut over without container restart; ACL and principal audit passed"
}

case "$MODE" in
  prepare) prepare_overlap ;;
  cutover) cutover_role ;;
esac
