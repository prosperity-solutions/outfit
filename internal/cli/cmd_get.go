package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/prosperity-solutions/outfit/internal/config"
)

type getResult struct {
	Key    string `json:"key"`
	Found  bool   `json:"found"`
	Value  string `json:"value"`
	Source string `json:"source"`
	Locked bool   `json:"locked"`
}

// cmdGet is the hot path hooks call: one git process, no writes, no warnings.
// An unset key prints nothing and exits 1, like `git config --get`, so
//
//	v=$(outfit get biome 2>/dev/null || git config -f .outfit/config --get outfit.biome)
//
// falls through to the committed default for non-users.
func cmdGet(ctx context.Context, e Env, args []string) int {
	fs := newFlags("get")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	if len(pos) != 1 {
		return report(e.Stdout, e.Stderr, *asJSON, Usagef("usage: outfit get <key>").WithFix("outfit get biome"))
	}
	key, err := config.NormalizeKey(pos[0])
	if err != nil || config.IsReserved(key) {
		if err == nil {
			err = fmt.Errorf("%q is not a toggle", key)
		}
		return report(e.Stdout, e.Stderr, *asJSON, Usagef("%v", err))
	}

	l, err := load(ctx, e)
	if err != nil {
		return report(e.Stdout, e.Stderr, *asJSON, err)
	}
	t, ok := l.Result.Get(key)
	if *asJSON {
		out, _ := json.Marshal(getResult{Key: key, Found: ok, Value: t.Value, Source: t.Source, Locked: t.Locked})
		fmt.Fprintln(e.Stdout, string(out))
	} else if ok {
		fmt.Fprintln(e.Stdout, t.Value)
	}
	if !ok {
		return ExitError
	}
	return ExitOK
}
