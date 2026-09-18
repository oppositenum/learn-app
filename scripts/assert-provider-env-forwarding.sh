#!/usr/bin/env bash
# Assert that the repository's Docker configuration can actually carry the
# provider channel settings into the API container.
#
# This exists because compose.prod.yaml and compose.deploy.yaml declare an
# explicit `environment:` map with no `env_file:`. A variable that is not listed
# there never reaches the container, so adding a channel variable to the Go
# config without adding it to both compose files silently produces a deployment
# where the channel cannot be configured at all — which is exactly what happened
# between bde19ec and 58643e4.
#
# The expected variable list is derived from the Go source rather than hardcoded
# here, so a channel variable added later is caught without editing this script.
#
# Uses only POSIX shell tooling: no yq, no Python packages, no npm dependencies.

set -u

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repository_root" || exit 1

tutor_config='server/cmd/api/tutor_provider_config.go'
content_config='server/cmd/api/content_provider_config.go'
compose_files='compose.prod.yaml compose.deploy.yaml'
production_example='.env.production.example'
reference_example='.env.example'

failures=0

fail() {
	printf 'FAIL %s\n' "$1"
	failures=$((failures + 1))
}

pass() {
	printf 'ok   %s\n' "$1"
}

for required in "$tutor_config" "$content_config" $compose_files "$production_example" "$reference_example"; do
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

# 1 & 2. Every channel variable is forwarded by both compose files, using
# compose interpolation rather than a literal value.
for compose_file in $compose_files; do
	missing=''
	not_interpolated=''
	for variable in $channel_variables; do
		line="$(grep -E "^[[:space:]]*${variable}:" "$compose_file" | head -n 1)"
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
		fail "$compose_file does not forward:$missing"
	else
		pass "$compose_file forwards all $channel_count channel variables"
	fi
	if [ -n "$not_interpolated" ]; then
		fail "$compose_file hardcodes instead of interpolating:$not_interpolated"
	else
		pass "$compose_file interpolates every channel variable"
	fi
done

# 3. The retired variables are gone from both compose files. Forwarding them
# would hand the API container a value that makes it refuse to start.
for compose_file in $compose_files; do
	present=''
	for variable in $retired_variables; do
		if grep -q "$variable" "$compose_file"; then
			present="$present $variable"
		fi
	done
	if [ -n "$present" ]; then
		fail "$compose_file still references retired:$present"
	else
		pass "$compose_file references no retired variable"
	fi
done

# 4 & 5. The production example teaches the current variables and none of the
# retired ones.
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

# 6. CONTENT_GENERATOR_IDENTITY shares a prefix with the content channel but is
# a separate placeholder read directly by main.go. It must not be treated as a
# channel variable, and it must survive this cleanup.
if printf '%s\n' "$channel_variables" | grep -qx 'CONTENT_GENERATOR_IDENTITY'; then
	fail "CONTENT_GENERATOR_IDENTITY was derived as a provider channel variable"
else
	pass "CONTENT_GENERATOR_IDENTITY is not treated as a provider channel variable"
fi
for file in $compose_files "$production_example"; do
	if grep -q 'CONTENT_GENERATOR_IDENTITY' "$file"; then
		pass "$file still carries CONTENT_GENERATOR_IDENTITY"
	else
		fail "$file dropped CONTENT_GENERATOR_IDENTITY"
	fi
done

# Defaults must mean the same thing in compose and in the reference example,
# so that a deployment behaves the same whether or not it sets the variable.
for compose_file in $compose_files; do
	mismatched=''
	for variable in $channel_variables; do
		compose_default="$(
			grep -E "^[[:space:]]*${variable}:" "$compose_file" | head -n 1 |
				sed -n "s/.*\${${variable}:-\([^}]*\)}.*/\1/p"
		)"
		example_default="$(
			grep -E "^${variable}=" "$reference_example" | head -n 1 | cut -d= -f2-
		)"
		if [ "$compose_default" != "$example_default" ]; then
			mismatched="$mismatched ${variable}(compose='${compose_default}' example='${example_default}')"
		fi
	done
	if [ -n "$mismatched" ]; then
		fail "$compose_file defaults disagree with $reference_example:$mismatched"
	else
		pass "$compose_file defaults agree with $reference_example"
	fi
done

# No API key may be committed. Every key variable must default to empty.
leaked=''
for compose_file in $compose_files; do
	for variable in $channel_variables; do
		case "$variable" in
		*_API_KEY) ;;
		*) continue ;;
		esac
		compose_default="$(
			grep -E "^[[:space:]]*${variable}:" "$compose_file" | head -n 1 |
				sed -n "s/.*\${${variable}:-\([^}]*\)}.*/\1/p"
		)"
		[ -n "$compose_default" ] && leaked="$leaked ${compose_file}:${variable}"
	done
done
if [ -n "$leaked" ]; then
	fail "an API key default is committed:$leaked"
else
	pass "every API key default is empty"
fi

# 7. Non-zero exit on any failure.
if [ "$failures" -ne 0 ]; then
	printf '\n%d check(s) failed.\n' "$failures"
	exit 1
fi
printf '\nAll provider env forwarding checks passed.\n'
