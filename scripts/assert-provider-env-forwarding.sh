#!/usr/bin/env bash
# Assert that a Docker compose file can actually carry the provider channel
# settings into the API container.
#
# This exists because the compose files declare an explicit `environment:` map
# with no `env_file:`. A variable that is not listed there never reaches the
# container, so adding a channel variable to the Go config without adding it to
# compose silently produces a deployment where the channel cannot be configured
# at all — which is exactly what happened between bde19ec and 58643e4.
#
# The expected variable list is derived from the Go source rather than hardcoded
# here, so a channel variable added later is caught without editing this script.
#
# Usage:
#   assert-provider-env-forwarding.sh
#       Check the repository's own configuration: both compose files, the
#       production example and the reference example.
#
#   assert-provider-env-forwarding.sh --compose-file PATH
#       Check one compose file only. Use this on a REDACTED local copy of the
#       server's /opt/learn-app/compose.yaml, which no repository check can
#       otherwise reach: the deploy gateway only reads that file and Test Deploy
#       only uploads images, so the two are never synchronised automatically.
#
# This script never connects to a server and never downloads anything. Taking
# and redacting the copy is the operator's job; the copy must contain no keys.
#
# Uses only POSIX shell tooling: no yq, no Python packages, no npm dependencies.

set -u

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repository_root" || exit 1

tutor_config='server/cmd/api/tutor_provider_config.go'
content_config='server/cmd/api/content_provider_config.go'
production_example='.env.production.example'
reference_example='.env.example'

usage() {
	cat <<'USAGE'
Usage:
  assert-provider-env-forwarding.sh
  assert-provider-env-forwarding.sh --compose-file PATH

  --compose-file PATH   Check a single compose file instead of the repository
                        defaults. Intended for a redacted copy of the server's
                        compose.yaml. The path may contain spaces.
  -h, --help            Show this message.
USAGE
}

single_compose=''
while [ "$#" -gt 0 ]; do
	case "$1" in
	--compose-file)
		if [ "$#" -lt 2 ]; then
			printf 'error: --compose-file requires a path\n' >&2
			usage >&2
			exit 2
		fi
		single_compose="$2"
		shift 2
		;;
	--compose-file=*)
		single_compose="${1#--compose-file=}"
		if [ -z "$single_compose" ]; then
			printf 'error: --compose-file requires a path\n' >&2
			exit 2
		fi
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		printf 'error: unknown argument %s\n' "$1" >&2
		usage >&2
		exit 2
		;;
	esac
done

failures=0

fail() {
	printf 'FAIL %s\n' "$1"
	failures=$((failures + 1))
}

pass() {
	printf 'ok   %s\n' "$1"
}

# Never echo a value that might be a credential, even when pointed at a copy
# that was supposed to be redacted.
redact() {
	case "$1" in
	*_API_KEY) printf '<redacted>' ;;
	*) printf '%s' "$2" ;;
	esac
}

for required in "$tutor_config" "$content_config" "$reference_example"; do
	if [ ! -f "$required" ]; then
		fail "$required is missing"
		exit 1
	fi
done

# Channel variables, straight from the Go constant blocks that read them.
channel_variables="$(
	grep -ho '"\(TUTOR\|CONTENT\)_\(GENERATOR\|REVIEWER\)_[A-Z_]*"' \
		"$tutor_config" "$content_config" |
		tr -d '"' | sort -u
)"

# Retired variables, straight from retiredContentEnvNames.
retired_variables="$(
	sed -n '/^var retiredContentEnvNames = \[\]string{/,/^}/p' "$content_config" |
		grep -o '"[A-Z_]*"' | tr -d '"' | sort -u
)"

channel_count="$(printf '%s\n' "$channel_variables" | grep -c .)"
retired_count="$(printf '%s\n' "$retired_variables" | grep -c .)"

if [ "$channel_count" -lt 28 ]; then
	fail "derived only $channel_count channel variables from the Go config; expected at least 28"
else
	pass "derived $channel_count channel variables from the Go config"
fi
if [ "$retired_count" -ne 2 ]; then
	fail "derived $retired_count retired variables; expected 2"
else
	pass "derived $retired_count retired variables from the Go config"
fi

# Read a variable's compose fallback, i.e. the DEFAULT in ${VAR:-DEFAULT}.
compose_default_of() {
	grep -E "^[[:space:]]*${2}:" "$1" | head -n 1 |
		sed -n "s/.*\${${2}:-\([^}]*\)}.*/\1/p"
}

# Every channel variable is forwarded, using compose interpolation rather than a
# literal value. Interpolation matters twice over: it keeps the value in the env
# file instead of the compose file, and it keeps secrets out of version control.
check_forwarding() {
	local file="$1" missing='' not_interpolated='' variable line
	for variable in $channel_variables; do
		line="$(grep -E "^[[:space:]]*${variable}:" "$file" | head -n 1)"
		if [ -z "$line" ]; then
			missing="$missing $variable"
			continue
		fi
		case "$line" in
		*"\${${variable}"*) ;;
		*) not_interpolated="$not_interpolated $variable" ;;
		esac
	done
	if [ -n "$missing" ]; then
		fail "$file does not forward:$missing"
	else
		pass "$file forwards all $channel_count channel variables"
	fi
	if [ -n "$not_interpolated" ]; then
		fail "$file hardcodes instead of interpolating:$not_interpolated"
	else
		pass "$file interpolates every channel variable"
	fi
}

# Forwarding a retired variable hands the API container a value that makes it
# refuse to start.
check_retired_absent() {
	local file="$1" present='' variable
	for variable in $retired_variables; do
		if grep -q "$variable" "$file"; then
			present="$present $variable"
		fi
	done
	if [ -n "$present" ]; then
		fail "$file still references retired:$present"
	else
		pass "$file references no retired variable"
	fi
}

# No API key may be committed or pasted into a compose file.
check_no_committed_keys() {
	local file="$1" leaked='' variable value
	for variable in $channel_variables; do
		case "$variable" in
		*_API_KEY) ;;
		*) continue ;;
		esac
		value="$(compose_default_of "$file" "$variable")"
		[ -n "$value" ] && leaked="$leaked $variable"
	done
	if [ -n "$leaked" ]; then
		fail "$file commits a non-empty API key default:$leaked"
	else
		pass "$file leaves every API key default empty"
	fi
}

# Defaults must mean the same thing here and in the reference example, so a
# deployment behaves the same whether or not it sets the variable.
check_defaults_match_reference() {
	local file="$1" mismatched='' variable here there
	for variable in $channel_variables; do
		here="$(compose_default_of "$file" "$variable")"
		there="$(grep -E "^${variable}=" "$reference_example" | head -n 1 | cut -d= -f2-)"
		if [ "$here" != "$there" ]; then
			mismatched="$mismatched ${variable}(this='$(redact "$variable" "$here")' example='$(redact "$variable" "$there")')"
		fi
	done
	if [ -n "$mismatched" ]; then
		fail "$file defaults disagree with $reference_example:$mismatched"
	else
		pass "$file defaults agree with $reference_example"
	fi
}

check_one_compose() {
	local file="$1"
	printf 'checking compose file: %s\n' "$file"
	check_forwarding "$file"
	check_retired_absent "$file"
	check_no_committed_keys "$file"
	check_defaults_match_reference "$file"
}

if [ -n "$single_compose" ]; then
	if [ ! -f "$single_compose" ]; then
		printf 'FAIL compose file not found: %s\n' "$single_compose"
		printf '\n1 check(s) failed.\n'
		exit 1
	fi
	check_one_compose "$single_compose"
	if [ "$failures" -ne 0 ]; then
		printf '\n%d check(s) failed.\n' "$failures"
		exit 1
	fi
	printf '\nAll provider env forwarding checks passed.\n'
	exit 0
fi

# Default mode: the repository's own configuration.
repository_composes=('compose.prod.yaml' 'compose.deploy.yaml')

for required in "${repository_composes[@]}" "$production_example"; do
	if [ ! -f "$required" ]; then
		fail "$required is missing"
		exit 1
	fi
done

for compose_file in "${repository_composes[@]}"; do
	check_one_compose "$compose_file"
done

# The production example teaches the current variables and none of the retired
# ones.
missing=''
for variable in $channel_variables; do
	grep -qE "^${variable}=" "$production_example" || missing="$missing $variable"
done
if [ -n "$missing" ]; then
	fail "$production_example is missing:$missing"
else
	pass "$production_example lists all $channel_count channel variables"
fi

present=''
for variable in $retired_variables; do
	grep -qE "^${variable}=" "$production_example" && present="$present $variable"
done
if [ -n "$present" ]; then
	fail "$production_example still assigns retired:$present"
else
	pass "$production_example assigns no retired variable"
fi

# CONTENT_GENERATOR_IDENTITY shares a prefix with the content channel but is a
# separate placeholder read directly by main.go. It must not be treated as a
# channel variable, and it must survive this cleanup.
if printf '%s\n' "$channel_variables" | grep -qx 'CONTENT_GENERATOR_IDENTITY'; then
	fail "CONTENT_GENERATOR_IDENTITY was derived as a provider channel variable"
else
	pass "CONTENT_GENERATOR_IDENTITY is not treated as a provider channel variable"
fi
for file in "${repository_composes[@]}" "$production_example"; do
	if grep -q 'CONTENT_GENERATOR_IDENTITY' "$file"; then
		pass "$file still carries CONTENT_GENERATOR_IDENTITY"
	else
		fail "$file dropped CONTENT_GENERATOR_IDENTITY"
	fi
done

if [ "$failures" -ne 0 ]; then
	printf '\n%d check(s) failed.\n' "$failures"
	exit 1
fi
printf '\nAll provider env forwarding checks passed.\n'
