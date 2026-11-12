#!/bin/sh
# bump-version.sh major|minor|patch|X.Y.Z[-pre]: set a new digisafe version in version.sh and
# everywhere it's copied. Run from the repo root (make bump-version does). patch on a
# pre-release releases it: 1.0.0-rc.1 becomes 1.0.0.
set -eu

. ./version.sh
old=$VERSION
semver='^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'
echo "$old" | grep -Eq "$semver" || { echo "version.sh: $old isn't a version (X.Y.Z)" >&2; exit 1; }

core=${old%%-*}
major=${core%%.*}; rest=${core#*.}; minor=${rest%%.*}; patch=${rest#*.}
case "${1:-}" in
	major) new="$((major + 1)).0.0" ;;
	minor) new="$major.$((minor + 1)).0" ;;
	patch) if [ "$core" != "$old" ]; then new=$core; else new="$major.$minor.$((patch + 1))"; fi ;;
	"") echo "usage: $0 major|minor|patch|X.Y.Z" >&2; exit 2 ;;
	*) new=${1#v} ;;
esac
echo "$new" | grep -Eq "$semver" || { echo "$new isn't a version: want X.Y.Z or X.Y.Z-pre" >&2; exit 1; }
[ "$new" != "$old" ] || { echo "the version is already $old" >&2; exit 1; }

# replace FROM with TO in FILE: exact text, every occurrence (no regex, so dots are dots)
replace() {
	file=$1 from=$2 to=$3
	grep -qF -- "$from" "$file" || { echo "$file: \"$from\" not found" >&2; exit 1; }
	tmp=$(mktemp)
	awk -v from="$from" -v to="$to" '{
		out = ""
		while ((i = index($0, from)) > 0) { out = out substr($0, 1, i - 1) to; $0 = substr($0, i + length(from)) }
		print out $0
	}' "$file" > "$tmp"
	cat "$tmp" > "$file" && rm -f "$tmp" # keeps the file's mode (version.sh stays executable)
}

# Where the version is copied. Only these patterns change: "0.1.0" alone also appears in go.sum
# and go.mod for unrelated modules.
replace version.sh "VERSION=$old" "VERSION=$new"
replace internal/app/app.go "const Version = \"$old\"" "const Version = \"$new\""
for doc in *.md; do
	if grep -qF -- "digisafe-$old-" "$doc"; then
		replace "$doc" "digisafe-$old-" "digisafe-$new-"
	fi
done

# nothing left behind
left=$(grep -nF -e "VERSION=$old" -e "const Version = \"$old\"" -e "digisafe-$old-" version.sh internal/app/app.go *.md || true)
[ -z "$left" ] || { echo "still $old:" >&2; echo "$left" >&2; exit 1; }

echo "version $old -> $new"
git --no-pager diff --stat 2>/dev/null || true
echo "next: make check, commit (\"version $new\"), merge to main, then make release"
