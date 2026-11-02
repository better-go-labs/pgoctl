#!/usr/bin/env bash
# Delete every release (and its tag) whose tag begins with the given prefix.
# Only this run's own prefix is ever passed; the profile/demo/* fixtures are
# refused outright so the e2e can never remove them.
set -euo pipefail

PREFIX="${1:?usage: cleanup-releases.sh <tag-prefix>}"

case "$PREFIX" in
	profile/demo | profile/demo/*)
		echo "refusing to clean the demo fixture prefix: $PREFIX" >&2
		exit 1
		;;
esac

tags=$(gh release list --repo "$GITHUB_REPOSITORY" --limit 200 --json tagName -q '.[].tagName' \
	| grep -E "^${PREFIX}/" || true)
for tag in $tags; do
	[ -n "$tag" ] || continue
	echo "deleting release $tag"
	gh release delete "$tag" --repo "$GITHUB_REPOSITORY" --yes --cleanup-tag || true
done
