# outfit

Run a git monorepo under a **profile**: which hook checks run, and a few swapped
files (`AGENTS.md`, `CLAUDE.md`…). Your teammates never notice, and `git status`
stays truthful.

> **Status: bootstrapping.** The full spec is [docs/HANDOVER.md](docs/HANDOVER.md).
> Features land one PR at a time; this README gets its install one-liner and demo
> in the docs PR.

## Commands (so far)

```sh
outfit get biome                 # one git process; exit 1 when unset
outfit set biome=off --global    # personal toggle; key= unsets; --here for one worktree
outfit status                    # active profile, where every value comes from
outfit ls                        # profiles in the committed / local / worktree tiers
outfit version
```

See [docs/config.md](docs/config.md). Profile switching, hooks and self-update land
in the next releases.

## Develop

Every change goes through the ship workflow: see [AGENTS.md](AGENTS.md) and
[docs/ship.md](docs/ship.md).

```sh
go build ./cmd/outfit && ./outfit version
gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...
```

MIT licensed.
