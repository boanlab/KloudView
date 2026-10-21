#!/usr/bin/env sh
# Generates the vault password file and the encrypted secrets it opens.
#
#   deploy/ansible/gen-vault.sh             # create both
#   deploy/ansible/gen-vault.sh --force     # replace both (see below first)
#
# The password file goes outside the repository, at the path ansible.cfg reads,
# so it cannot be committed. The encrypted vault goes into the inventory; it
# belongs to this deployment, not to the project, so keep it out of this
# repository too. Nothing secret is printed: read values back with
# `ansible-vault view`.
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
PASS_FILE="${KLOUDVIEW_VAULT_PASS_FILE:-$HOME/.ansible/kloudview-vault-pass}"
VAULT="${KLOUDVIEW_VAULT_FILE:-$HERE/inventory/group_vars/all/vault.yml}"
FORCE=0
while [ $# -gt 0 ]; do
	case "$1" in
	--force) FORCE=1 ;;
	-h | --help)
		sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*) echo "unknown option: $1" >&2; exit 2 ;;
	esac
	shift
done

command -v openssl >/dev/null || { echo "openssl is required" >&2; exit 1; }
command -v ansible-vault >/dev/null || { echo "ansible-vault is required (install ansible-core)" >&2; exit 1; }

if [ "$FORCE" -eq 0 ]; then
	for f in "$PASS_FILE" "$VAULT"; do
		if [ -e "$f" ]; then
			# Replacing the secrets of a deployment that is running breaks it:
			# the database keeps the password it was initialised with, and
			# enrolled agents hold credentials bound to the old key.
			echo "$f already exists. Pass --force to replace it." >&2
			echo "Replacing the secrets of a running deployment locks out its database and every enrolled agent." >&2
			exit 1
		fi
	done
fi

umask 077
mkdir -p "$(dirname "$PASS_FILE")" "$(dirname "$VAULT")"
# Both are written beside their destinations and moved into place only once
# encryption succeeded, so a failed run leaves nothing to refuse the next one.
PASS_NEW="$PASS_FILE.new.$$"
VAULT_NEW="$VAULT.new.$$"
PLAIN=$(mktemp)
trap 'rm -f "$PLAIN" "$PASS_NEW" "$VAULT_NEW"' EXIT
openssl rand -base64 48 > "$PASS_NEW"

# Lengths follow the server's rules, as tools/gen-env.sh does: the token needs
# 16 characters and the credential key 32, and the two must differ.
cat > "$PLAIN" <<SECRETS
# Generated once. Do not regenerate on a running deployment: the database keeps
# the password it was initialised with, and enrolled agents hold credentials
# bound to the key.
vault_kloudview_enrollment_token: "$(openssl rand -hex 16)"
vault_kloudview_agent_credential_key: "$(openssl rand -hex 32)"
vault_kloudview_postgres_password: "$(openssl rand -hex 24)"
vault_kloudview_admin_password: "$(openssl rand -hex 12)"
SECRETS
# The environment replaces ansible.cfg's password file rather than adding a
# second one, which ansible-vault would refuse to choose between.
ANSIBLE_VAULT_PASSWORD_FILE="$PASS_NEW" ansible-vault encrypt --output "$VAULT_NEW" "$PLAIN" >/dev/null
if command -v shred >/dev/null; then shred -u "$PLAIN"; fi
mv -f "$PASS_NEW" "$PASS_FILE"
mv -f "$VAULT_NEW" "$VAULT"

echo "wrote $PASS_FILE (mode 600, outside the repository)"
echo "wrote $VAULT (encrypted)"
echo
echo "Back up $PASS_FILE now: without it the vault cannot be opened."
echo "First admin password: ansible-vault view $VAULT"
