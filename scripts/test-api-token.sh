#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
tmp_dir=$(mktemp -d)
trap 'rm -rf -- "$tmp_dir"' EXIT
mkdir "$tmp_dir/bin"

cat > "$tmp_dir/bin/curl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$CURL_ARGS_FILE"
cat > "$CURL_CONFIG_FILE"
printf '\n204'
EOF
chmod +x "$tmp_dir/bin/curl"

export PATH="$tmp_dir/bin:$PATH"
export CURL_ARGS_FILE="$tmp_dir/args"
export CURL_CONFIG_FILE="$tmp_dir/config"
printf 'audit:dummy-password\n' > "$tmp_dir/credentials"

fail() {
  printf 'test-api-token.sh: %s\n' "$*" >&2
  exit 1
}

has_arg() {
  local expected=$1 arg
  while IFS= read -r arg; do
    [[ $arg == "$expected" ]] && return 0
  done < "$CURL_ARGS_FILE"
  return 1
}

reject_url() {
  local url=$1
  rm -f -- "$CURL_ARGS_FILE" "$CURL_CONFIG_FILE"
  if MYMAIL_URL='' MYMAIL_USER='' "$script_dir/api-token.sh" --url "$url" \
    --credentials "$tmp_dir/does-not-exist" revoke test > "$tmp_dir/out" 2> "$tmp_dir/err"; then
    fail "accepted unsafe URL: $url"
  fi
  [[ ! -e $CURL_ARGS_FILE && ! -e $CURL_CONFIG_FILE ]] || fail "curl ran for rejected URL: $url"
  [[ $(< "$tmp_dir/err") == *'URL'* ]] || fail "URL was not rejected before credentials: $url"
}

reject_url 'http://example.com'
reject_url 'http://192.0.2.1:8080'
reject_url 'http://localhost:8080'
reject_url 'http://127.0.0.1.evil.example'
reject_url 'http://127.0.0.1@evil.example'
reject_url 'http://127.0.0.1:80@evil.example'
reject_url 'http://[::1]@evil.example'
reject_url 'http://127.0.0.1\\@evil.example'
reject_url 'http://127.0.0.1%2eevil.example'
reject_url 'http://127.0.0.1:bad'
reject_url 'http://127.0.0.1?redirect=evil'
reject_url 'https://user:pass@example.com'
reject_url 'https://example.com\evil.example'

for url in 'http://127.0.0.1:8080' 'http://[::1]:8080' 'https://mail.example.com/mymail'; do
  rm -f -- "$CURL_ARGS_FILE" "$CURL_CONFIG_FILE"
  MYMAIL_URL='' MYMAIL_USER='' "$script_dir/api-token.sh" --url "$url" \
    --credentials "$tmp_dir/credentials" revoke test > "$tmp_dir/out" 2> "$tmp_dir/err" ||
    fail "rejected safe URL: $url"
  has_arg '--disable' || fail "curl config was not disabled: $url"
  has_arg '--max-redirs' || fail "redirects were not disabled: $url"
  has_arg "$url/api/v1/tokens/test" || fail "wrong request URL: $url"
  [[ $(< "$CURL_CONFIG_FILE") == 'user = "audit:dummy-password"' ]] ||
    fail "credentials did not reach curl config: $url"
  if [[ $url == http://* ]]; then
    has_arg '=http' || fail "curl protocol was not restricted to HTTP: $url"
    has_arg '--noproxy' || fail "loopback HTTP could use a proxy: $url"
  else
    has_arg '=https' || fail "curl protocol was not restricted to HTTPS: $url"
  fi
done

printf 'test-api-token.sh: passed\n'
