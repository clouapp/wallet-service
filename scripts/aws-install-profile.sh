#!/usr/bin/env bash
# Installs, on the host it runs on, the AWS profile of the dev stack's server user
# (template.dev.yaml, ServerAccessKeySecret). The secret is read on stdin, as
# {"aws_access_key_id": "…", "aws_secret_access_key": "…"}, so neither key reaches
# argv, the terminal or a log. Only the named profile is written: the default
# profile and every other one stay as they are.
#
# Usage: aws-install-profile.sh <profile> <region>      (needs AWS CLI v2)
# Run by `make server-credentials`, which pipes the secret over ssh.
set -euo pipefail

usage="usage: aws-install-profile.sh <profile> <region>"
profile="${1:?$usage}"
region="${2:?$usage}"

json="$(cat)"
id_pattern='"aws_access_key_id"[[:space:]]*:[[:space:]]*"([A-Z0-9]+)"'
secret_pattern='"aws_secret_access_key"[[:space:]]*:[[:space:]]*"([A-Za-z0-9/+=]+)"'

if [[ ! $json =~ $id_pattern ]]; then
	echo "aws-install-profile: no aws_access_key_id on stdin" >&2
	exit 1
fi
key_id="${BASH_REMATCH[1]}"

if [[ ! $json =~ $secret_pattern ]]; then
	echo "aws-install-profile: no aws_secret_access_key on stdin" >&2
	exit 1
fi
secret_key="${BASH_REMATCH[1]}"
unset json

# `aws configure import` takes the IAM console's CSV layout and names the profile
# after the "User Name" column. The file only lives for this command.
umask 077
csv="$(mktemp)"
trap 'rm -f "$csv"' EXIT
printf 'User Name,Access key ID,Secret access key\n%s,%s,%s\n' "$profile" "$key_id" "$secret_key" >"$csv"
unset secret_key

aws configure import --csv "file://$csv" >/dev/null
aws configure set region "$region" --profile "$profile"

echo "aws-install-profile: profile $profile installed (access key …${key_id: -4}, region $region)"
