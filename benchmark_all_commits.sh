#!/usr/bin/env bash

set -euo pipefail

source=$(pwd)
tmp=$(mktemp -d)
cp -r ./ "$tmp/"
cd "$tmp"

# find commits since the beginning of time
for commit in $(git log --reverse --pretty=format:"%h"); do
	echo ""
	echo "Benchmarking commit $commit"
	echo ""

	git checkout "$commit"
	message=$(git log --format=%B -n 1 "$commit")

	output=$(go test -run=^a -bench=. ./... || true)
	echo "$output" | gobenchdata -v "$commit" -t "$message" -a --json "$source/benchmarks.json" || true
done

rm -rf "$tmp"
