#!/usr/bin/env bash
# Example external grove recipe: open companion URLs (JIRA ticket + GitHub PRs)
# derived from the branch name on the workstation via wsm POST /open-url.
#
# Install: copy to a directory on your PATH as grove-recipe-company-links
#          chmod +x grove-recipe-company-links
#
# grove.json (runs after the webhook recipe so the IDE/desktop exists first):
#   { "type": "company-links", "url": "http://127.0.0.1:39788/open-url", "token": "$GROVE_WEBHOOK_TOKEN" }
#
# Requires: curl, jq; gh (optional, for PR lookup)

set -euo pipefail

name="${GROVE_NAME:-$GROVE_BRANCH}"
endpoint="${GROVE_RECIPE_URL:?}"
token="${GROVE_RECIPE_TOKEN:-}"

open_url() {
  curl -fsS -X POST "$endpoint" \
    -H "authorization: Bearer $token" \
    -H 'content-type: application/json' \
    -d "$(jq -n --arg n "$name" --arg u "$1" '{name:$n,url:$u}')" >/dev/null || true
}

# JIRA: jiraproject-12345-... -> JIRAPROJECT-12345
if [[ "$GROVE_BRANCH" =~ ^([a-zA-Z]+-[0-9]+) ]]; then
  ticket="${BASH_REMATCH[1]^^}"
  open_url "https://jira.mycompany.com/browse/${ticket}"
fi

# PRs whose head is this branch
if command -v gh >/dev/null 2>&1; then
  gh pr list --head "$GROVE_BRANCH" --state open --json url --jq '.[].url' \
    | while read -r u; do
        [[ -n "$u" ]] && open_url "$u"
      done
fi
