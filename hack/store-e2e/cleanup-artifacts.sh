#!/usr/bin/env bash
# Delete every Actions artifact whose name begins with the wire form of the
# given tag prefix (slashes become underscores). Only this run's own prefix is
# ever passed.
set -euo pipefail

PREFIX="${1:?usage: cleanup-artifacts.sh <tag-prefix>}"
WIRE=${PREFIX//\//_}

ids=$(gh api --paginate "repos/$GITHUB_REPOSITORY/actions/artifacts" \
	-q ".artifacts[] | select(.name | startswith(\"$WIRE\")) | .id" || true)
for id in $ids; do
	[ -n "$id" ] || continue
	echo "deleting artifact $id"
	gh api -X DELETE "repos/$GITHUB_REPOSITORY/actions/artifacts/$id" || true
done
