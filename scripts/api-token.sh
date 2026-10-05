#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  api-token.sh [--url BASE_URL] [--user USER | --credentials FILE|-] create --name NAME --lifetime NUMBER[s|m|h|d] --folders ID[,ID...]
  api-token.sh [--url BASE_URL] [--user USER | --credentials FILE|-] revoke SLUG

The default BASE_URL is http://127.0.0.1:8080. --user prompts for the Basic
Auth password; --credentials reads one username:password line from a file, or
from standard input when FILE is -. Create prints only the
token secret to stdout and its revocation slug to stderr.
Remote URLs must use HTTPS. Plain HTTP is allowed only for literal loopback
hosts 127.0.0.1 and [::1]. URLs must not contain embedded credentials.
MYMAIL_URL and MYMAIL_USER provide defaults for --url and --user.
EOF
}

die() {
  printf 'api-token.sh: %s\n' "$*" >&2
  exit 1
}

base_url=${MYMAIL_URL:-http://127.0.0.1:8080}
username=${MYMAIL_USER:-}
user_given=false
credentials_source=
credentials_given=false
command_name=
token_slug=
name=
lifetime=
folders=

while (($#)); do
  case $1 in
    create|revoke)
      [[ -z $command_name ]] || die 'choose one command'
      command_name=$1
      shift
      ;;
    --url|--user|--credentials|--name|--lifetime|--folders)
      option=$1
      (($# >= 2)) || die "$option needs a value"
      value=$2
      case $option in
        --url) base_url=$value ;;
        --user) username=$value; user_given=true ;;
        --credentials) credentials_source=$value; credentials_given=true ;;
        --name) name=$value ;;
        --lifetime) lifetime=$value ;;
        --folders) folders=$value ;;
      esac
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      if [[ $command_name == revoke && -z $token_slug ]]; then
        token_slug=$1
        shift
      else
        die "unexpected argument: $1"
      fi
      ;;
  esac
done

[[ -n $command_name ]] || { usage >&2; exit 2; }
[[ $base_url == http://* || $base_url == https://* ]] || die 'URL must start with http:// or https://'
[[ $base_url != *'?'* && $base_url != *'#'* ]] || die 'URL must not contain a query or fragment'
[[ ! $base_url =~ [[:cntrl:][:space:]\\] ]] || die 'URL must not contain whitespace, control characters, or backslashes'
url_scheme=${base_url%%://*}
url_remainder=${base_url#*://}
url_authority=${url_remainder%%/*}
[[ -n $url_authority ]] || die 'URL must have a host'
if [[ $url_authority == \[* ]]; then
  [[ $url_authority =~ ^\[([0-9A-Fa-f:.]+)\](:[0-9]+)?$ ]] || die 'invalid URL authority'
  url_host=${BASH_REMATCH[1]}
  # curl validates the full IPv6 address; brackets and port delimiters are
  # checked here before deciding whether plain HTTP is safe.
  [[ $url_host == *:* ]] || die 'invalid URL authority'
  url_host="[$url_host]"
else
  [[ $url_authority =~ ^([A-Za-z0-9.-]+)(:[0-9]+)?$ ]] || die 'invalid URL authority'
  url_host=${BASH_REMATCH[1]}
fi
if [[ $url_scheme == http ]]; then
  [[ $url_host == 127.0.0.1 || $url_host == '[::1]' ]] ||
    die 'plain HTTP is allowed only for 127.0.0.1 or [::1]; use HTTPS for remote URLs'
fi
base_url=${base_url%/}
[[ $user_given != true || $credentials_given != true ]] || die 'use either --user or --credentials'
if [[ $credentials_given == true ]]; then username=; fi
[[ $username != *:* ]] || die '--user expects a username only; use --credentials for unattended authentication'

curl_args=(--silent --show-error --connect-timeout 5 --max-time 30 --header 'Accept: application/json')
curl_args+=(--proto "=$url_scheme" --proto-redir '=https' --max-redirs 0)
if [[ $url_scheme == http ]]; then
  curl_args+=(--noproxy '*')
fi
if [[ $credentials_given == true ]]; then
  credentials=
  if [[ $credentials_source == - ]]; then
    if [[ -t 0 ]]; then
      printf 'Username:password: ' >&2
      IFS= read -rs credentials || :
      printf '\n' >&2
      [[ -n $credentials ]] || die 'standard input has no credentials'
    else
      IFS= read -r credentials || [[ -n $credentials ]] || die 'standard input has no credentials'
    fi
  else
    [[ -r $credentials_source && ! -d $credentials_source ]] || die "cannot read credentials file: $credentials_source"
    IFS= read -r credentials < "$credentials_source" || [[ -n $credentials ]] || die 'credentials file is empty'
  fi
  credentials=${credentials%$'\r'}
  [[ $credentials == *:* && -n ${credentials%%:*} && -n ${credentials#*:} && ! $credentials =~ [[:cntrl:]] ]] ||
    die 'credentials must be one username:password line without control characters'
  curl_args+=(--basic)
elif [[ -n $username ]]; then
  curl_args+=(--basic --user "$username")
fi

if [[ $command_name == create ]]; then
  [[ -n $name && -n $lifetime && -n $folders ]] || die 'create needs --name, --lifetime, and --folders'
  name_bytes=$(printf '%s' "$name" | wc -c)
  [[ $name_bytes -le 200 && ! $name =~ [[:cntrl:]] ]] || die 'name must be 1–200 bytes without control characters'
  [[ $folders =~ ^[1-9][0-9]*(,[1-9][0-9]*)*$ ]] || die 'folders must be comma-separated positive IDs'
  [[ $lifetime =~ ^([1-9][0-9]*)([smhd])$ ]] || die 'lifetime must be a positive number followed by s, m, h, or d'
  amount=${BASH_REMATCH[1]}
  unit=${BASH_REMATCH[2]}
  [[ ${#amount} -le 9 ]] || die 'lifetime exceeds ten years'
  case $unit in
    s) multiplier=1 ;;
    m) multiplier=60 ;;
    h) multiplier=3600 ;;
    d) multiplier=86400 ;;
  esac
  ((10#$amount <= 315360000 / multiplier)) || die 'lifetime exceeds ten years'
  seconds=$((10#$amount * multiplier))
  expiry_epoch=$(( $(date -u +%s) + seconds ))
  if expires_at=$(date -u -d "@$expiry_epoch" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null); then
    : # GNU date
  elif expires_at=$(date -u -r "$expiry_epoch" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null); then
    : # BSD/macOS date
  else
    die 'date cannot format the expiry time'
  fi
  backslash=$'\\'
  escaped_backslash=$'\\\\'
  json_name=${name//"$backslash"/"$escaped_backslash"}
  json_name=${json_name//\"/\\\"}
  payload=$(printf '{"name":"%s","expires_at":"%s","folder_ids":[%s]}' "$json_name" "$expires_at" "$folders")
  curl_args+=(--request POST --header 'Content-Type: application/json' --data "$payload")
  endpoint=$base_url/api/v1/tokens
else
  [[ -z $name && -z $lifetime && -z $folders ]] || die 'revoke takes only a token slug'
  [[ $token_slug =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] || die 'revoke needs a token slug'
  curl_args+=(--request DELETE)
  endpoint=$base_url/api/v1/tokens/$token_slug
fi

# curl writes the status on its own final line. The body stays in memory, so a
# newly created secret is never written to a temporary file.
if [[ $credentials_given == true ]]; then
  # Feed curl its config on stdin so the password stays out of command arguments.
  backslash=$'\\'
  escaped_backslash=$'\\\\'
  config_credentials=${credentials//"$backslash"/"$escaped_backslash"}
  config_credentials=${config_credentials//\"/\\\"}
  response=$(printf 'user = "%s"\n' "$config_credentials" |
    curl --disable --config - "${curl_args[@]}" --write-out $'\n%{http_code}' "$endpoint") || die 'request failed'
else
  response=$(curl --disable "${curl_args[@]}" --write-out $'\n%{http_code}' "$endpoint") || die 'request failed'
fi
status=${response##*$'\n'}
body=${response%$'\n'*}

if [[ $command_name == create ]]; then
  [[ $status == 201 ]] || die "HTTP $status: $body"
  token_pattern='"token"[[:space:]]*:[[:space:]]*"(mymail_[A-Za-z0-9_-]{43})"'
  slug_pattern='"slug"[[:space:]]*:[[:space:]]*"([a-z0-9]+(-[a-z0-9]+)*)"'
  [[ $body =~ $token_pattern ]] || die 'server response has no token'
  token=${BASH_REMATCH[1]}
  [[ $body =~ $slug_pattern ]] || die 'server response has no token slug'
  printf 'Token slug: %s\n' "${BASH_REMATCH[1]}" >&2
  printf '%s\n' "$token"
else
  [[ $status == 204 ]] || die "HTTP $status: $body"
  printf 'Revoked token %s\n' "$token_slug" >&2
fi
