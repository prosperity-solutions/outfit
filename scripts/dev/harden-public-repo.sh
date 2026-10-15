#!/bin/sh
# Hardening for the public repository prosperity-solutions/outfit. Run once, as a repository admin.
set -e
R=repos/prosperity-solutions/outfit
# The token every workflow run gets starts read-only (each workflow asks for what it needs).
gh api -X PUT $R/actions/permissions/workflow -f default_workflow_permissions=read -F can_approve_pull_request_reviews=false
# Only actions pinned by commit SHA may run (all of this repository's are).
gh api -X PUT $R/actions/permissions -F enabled=true -f allowed_actions=all -F sha_pinning_required=true
# The release environment: only `main` may deploy to it. (It protects nothing until the signing
# secrets are moved into the environment by hand: this script never touches a secret.)
gh api -X PUT $R/environments/release -F 'deployment_branch_policy[protected_branches]=false' -F 'deployment_branch_policy[custom_branch_policies]=true'
gh api $R/environments/release/deployment-branch-policies --jq '.branch_policies[].name' | grep -qx main ||
  gh api -X POST $R/environments/release/deployment-branch-policies -f name=main -f type=branch
# Leaked-secret detection, and pull requests for vulnerable dependencies.
gh api -X PATCH $R -F 'security_and_analysis[secret_scanning][status]=enabled' \
  -F 'security_and_analysis[secret_scanning_push_protection][status]=enabled' \
  -F 'security_and_analysis[dependabot_security_updates][status]=enabled'
# A private channel for security reports (SECURITY.md points to it).
gh api -X PUT $R/private-vulnerability-reporting
