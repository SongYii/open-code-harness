#!/usr/bin/env bash
# Report the outcome of a scheduled CI run into a single tracking issue.
#
# Why this exists: the nightly lanes are the only place several gates run at
# all -- the full Context evaluation matrix, the repeated and soaked race
# suites, and the network-dependent citation check. A failure there is
# visible only to whoever opens the Actions tab, and nothing makes anyone do
# that. Between 2026-09-07 and 2026-09-12 every scheduled run failed, one of
# them on a real product defect (manual compaction silently covering nothing)
# that reached main and stayed there for four nights while the pull-request
# lane stayed green. This turns that signal into a notification the
# repository owner actually receives.
#
# One open issue at a time, identified by its label, so an ongoing outage
# accumulates dated comments on one thread instead of opening a new issue
# every night. A clean run closes it.
#
# Usage:
#   report-nightly-result.sh <run-url> [failed-job ...]
#
# Requires gh authenticated with issues: write.

set -euo pipefail

readonly LABEL="nightly-failure"
readonly TITLE="Nightly CI is failing"

run_url="${1:?run URL required}"
shift || true
failed=("$@")

existing="$(gh issue list --label "$LABEL" --state open --limit 1 --json number --jq '.[0].number // empty')"

if [ "${#failed[@]}" -eq 0 ]; then
	if [ -n "$existing" ]; then
		gh issue comment "$existing" --body "Scheduled CI is green again.

Run: $run_url"
		gh issue close "$existing"
		echo "closed #$existing: scheduled CI is green again"
	else
		echo "scheduled CI is green and no tracking issue is open; nothing to do"
	fi
	exit 0
fi

body="Scheduled CI failed.

Failing jobs:
"
for job in "${failed[@]}"; do
	body="$body- \`$job\`
"
done
body="$body
Run: $run_url

These lanes run only on the nightly schedule, so a failure here is not
visible on any pull request. This issue is opened and closed automatically by
\`scripts/report-nightly-result.sh\`; it closes itself on the next green
scheduled run."

if [ -n "$existing" ]; then
	gh issue comment "$existing" --body "$body"
	echo "commented on #$existing: ${failed[*]}"
	exit 0
fi

# The label may not exist yet in a fresh repository or fork.
gh label create "$LABEL" --description "A scheduled CI run failed" --color B60205 >/dev/null 2>&1 || true
gh issue create --title "$TITLE" --label "$LABEL" --body "$body"
echo "opened a tracking issue: ${failed[*]}"
