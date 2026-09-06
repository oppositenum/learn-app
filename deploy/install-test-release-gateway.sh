#!/usr/bin/env bash

set -Eeuo pipefail
umask 077

if [[ "${EUID}" -ne 0 || "$#" -ne 1 ]]; then
  printf 'Usage: sudo %s /path/to/github-actions-deploy-key.pub\n' "$0" >&2
  exit 2
fi

readonly public_key_file="$1"
readonly deploy_user="learnapp-deploy"
readonly gateway_source="$(cd "$(dirname "$0")" && pwd)/learn-app-test-release"
readonly gateway_target="/usr/local/sbin/learn-app-test-release"
readonly sudoers_file="/etc/sudoers.d/learn-app-test-release"

[[ -s "$public_key_file" ]] || { printf 'Public key file is missing or empty.\n' >&2; exit 1; }
[[ -x "$gateway_source" ]] || { printf 'Gateway source must exist and be executable: %s\n' "$gateway_source" >&2; exit 1; }

key_type="$(awk 'NR == 1 { print $1 }' "$public_key_file")"
key_body="$(awk 'NR == 1 { print $2 }' "$public_key_file")"
[[ "$key_type" == "ssh-ed25519" && -n "$key_body" ]] || {
  printf 'Only a single Ed25519 public key is accepted.\n' >&2
  exit 1
}
printf '%s %s\n' "$key_type" "$key_body" | ssh-keygen -l -f - >/dev/null

if ! id "$deploy_user" >/dev/null 2>&1; then
  useradd --create-home --home-dir "/var/lib/${deploy_user}" --shell /bin/bash "$deploy_user"
fi
passwd --lock "$deploy_user" >/dev/null

install -o root -g root -m 0755 "$gateway_source" "$gateway_target"
install -d -o "$deploy_user" -g "$deploy_user" -m 0700 "/var/lib/${deploy_user}/.ssh"
printf 'restrict,command="sudo -n %s" %s %s\n' "$gateway_target" "$key_type" "$key_body" \
  >"/var/lib/${deploy_user}/.ssh/authorized_keys"
chown "$deploy_user:$deploy_user" "/var/lib/${deploy_user}/.ssh/authorized_keys"
chmod 0600 "/var/lib/${deploy_user}/.ssh/authorized_keys"

temporary_sudoers="$(mktemp /etc/sudoers.d/learn-app-test-release.XXXXXX)"
{
  printf 'Defaults:%s env_keep += "SSH_ORIGINAL_COMMAND"\n' "$deploy_user"
  printf '%s ALL=(root) NOPASSWD: %s\n' "$deploy_user" "$gateway_target"
} >"$temporary_sudoers"
chmod 0440 "$temporary_sudoers"
visudo -cf "$temporary_sudoers" >/dev/null
mv -f "$temporary_sudoers" "$sudoers_file"

printf 'Installed restricted test-release gateway for %s.\n' "$deploy_user"
printf 'The key can invoke only upload, deploy, rollback, and status through %s.\n' "$gateway_target"
