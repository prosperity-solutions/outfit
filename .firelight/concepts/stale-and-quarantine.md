---
format: 1
title: Stale base and quarantine
summary: Stale means a pull changed the team's file under your swap; quarantine means both copies changed. outfit keeps both and never overwrites your bytes.
aliases: [stale, quarantine, outfit diff, outfit ack, conflict, exit 3, exit 4, out of sync]
tags: [troubleshooting]
related: [fix-out-of-sync, swapped-files]
covers: [internal/apply/**, internal/cli/cmd_stale.go, internal/cli/cmd_status.go, skills/topics/troubleshooting.md]
---
- **Stale**: a pull, merge or checkout changed a swapped **base** file. Your swap stays.
  `outfit diff <path>` shows the change; `outfit ack <path>` accepts it.
- **Quarantine**: the swapped file and the profile's copy both changed. Both are saved under
  `.git/outfit/quarantine/<time>-<key>/` and the command exits 3. A person decides.
- `outfit status` exits 0 (fine), 3 (a person must look) or 4 (out of sync or stale).

## What it is not
- Not data loss: nothing is overwritten. Never delete `.git/outfit/` by hand; use `outfit uninstall`.
