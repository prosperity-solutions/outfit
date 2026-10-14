package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/prosperity-solutions/outfit/internal/update"
)

// installScript installs the latest release; installCommand does so into a folder
// the user owns, for when outfit cannot replace itself where it lives.
const (
	installScript  = `curl -fsSL -H "Authorization: Bearer $(gh auth token)" https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh`
	installCommand = `curl -fsSL -H "Authorization: Bearer $(gh auth token)" https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | OUTFIT_INSTALL_DIR="$HOME/.local/bin" sh`
)

// updateClient builds the release client from the environment: the token from
// OUTFIT_GITHUB_TOKEN, GH_TOKEN, GITHUB_TOKEN or `gh auth token`, the two bases
// from OUTFIT_BASE_URL and OUTFIT_API_URL (for tests), the keys compiled in.
func updateClient(ctx context.Context, e Env) (*update.Client, string) {
	var gh func() string
	if e.GHToken != nil {
		gh = func() string { return e.GHToken(ctx) }
	}
	tok, ignored := update.FindToken(e.Getenv, gh)
	keys := e.UpdateKeys
	if keys == nil {
		keys = update.Keys()
	}
	return &update.Client{
		BaseURL: e.Getenv("OUTFIT_BASE_URL"),
		APIURL:  e.Getenv("OUTFIT_API_URL"),
		Token:   tok,
		HTTP:    e.HTTP,
		Keys:    keys,
	}, ignored
}

// asCLIError turns an update failure into a CLI error carrying its fix.
func asCLIError(err error) error {
	var ue *update.Error
	if errors.As(err, &ue) {
		fix := ue.Fix
		if fix == "" {
			fix = "outfit update --check   (see what is published; if it persists, reinstall: docs/install.md)"
		}
		e := Errorf("%s", ue.Msg).WithFix(fix)
		e.Retryable = ue.Retryable
		return e
	}
	var nw *update.NotWritableError
	if errors.As(err, &nw) {
		return (&Error{Code: ExitNeedsHuman,
			Msg: nw.Error() + " (outfit never uses sudo). The command below installs the latest release into a folder you own; " +
				"then put that folder before " + nw.Dir + " on your PATH, and run `outfit init` in each repository so the hook shims point at the new copy"}).
			WithFix(installCommand)
	}
	return Errorf("%v", err).WithFix("outfit update --check")
}

type updateJSON struct {
	Current         string `json:"current"`
	Latest          string `json:"latest,omitempty"`
	UpdateAvailable *bool  `json:"update_available,omitempty"`
	Updated         bool   `json:"updated"`
	Installed       string `json:"installed,omitempty"`
	Path            string `json:"path,omitempty"`
	Note            string `json:"note,omitempty"`
}

// cmdUpdate installs the latest release, or exactly --version (downgrades
// allowed), after verifying its minisign signature and SHA-256. --check only
// looks. It replaces the binary atomically in place; hooks keep working because
// the shims point at the same path.
func cmdUpdate(ctx context.Context, e Env, args []string) int {
	fs := newFlags("update")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	check := fs.Bool("check", false, "only report whether a newer release exists; change nothing")
	version := fs.String("version", "", "install exactly this release, e.g. v0.2.0 (downgrades allowed)")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	if code, bad := noArgs(e, "update", pos, args); bad {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, *asJSON, err) }
	emit := func(j updateJSON, human string) int {
		if *asJSON {
			b, _ := json.Marshal(j)
			fmt.Fprintln(e.Stdout, string(b))
		} else if human != "" {
			fmt.Fprintln(e.Stdout, human)
		}
		return ExitOK
	}

	if *check && *version != "" {
		return fail(Usagef("--check and --version cannot be combined").WithFix("outfit update --check"))
	}
	var want *update.Version
	if *version != "" {
		v, err := update.ParseVersion(*version)
		if err != nil {
			return fail(Usagef("%v", err).WithFix("outfit update --check   (shows the latest release)"))
		}
		want = &v
	}
	cur, curErr := update.ParseVersion(e.Build.Version)
	dev := curErr != nil

	client, ignored := updateClient(ctx, e)
	if ignored != "" {
		fmt.Fprintln(e.Stderr, "warning: "+ignored)
	}

	if *check {
		latest, err := client.LatestVerified(ctx)
		if err != nil {
			return fail(asCLIError(err))
		}
		avail := dev || cur.Less(latest)
		j := updateJSON{Current: e.Build.Version, Latest: latest.String(), UpdateAvailable: &avail}
		switch {
		case dev:
			// A plain `outfit update` refuses on a development build.
			return emit(j, fmt.Sprintf("Latest release: %s (this is a development build; `outfit update --version %s` installs it)", latest, latest))
		case !avail:
			return emit(j, fmt.Sprintf("outfit %s is up to date", cur))
		}
		return emit(j, fmt.Sprintf("Update available: %s → %s. Run outfit update", e.Build.Version, latest))
	}

	if dev && want == nil {
		return fail(Errorf("this is a development build (version %q); `outfit update` would replace it with a release", e.Build.Version).
			WithFix("outfit update --check   (names the latest release; then outfit update --version <it>), or install one: " + installScript))
	}
	if e.Exe == "" {
		return fail(Errorf("cannot tell where the running outfit lives").WithFix(installCommand))
	}

	// One update at a time: a second exits 0 with a note.
	if dir := update.CacheDir(e.Getenv); dir != "" {
		unlock, err := update.Lock(dir)
		switch {
		case errors.Is(err, update.ErrLocked):
			return emit(updateJSON{Current: e.Build.Version, Note: "another update is running"}, "another outfit update is already running; nothing to do")
		case err != nil:
			fmt.Fprintf(e.Stderr, "warning: could not take the update lock: %v\n", err)
		default:
			defer unlock()
		}
	}

	if want == nil {
		// Latest: a cheap, signature-checked look first, so an up-to-date outfit downloads
		// no archive but cannot be made to believe it is up to date by an unsigned answer.
		latest, err := client.LatestVerified(ctx)
		if err != nil {
			return fail(asCLIError(err))
		}
		if !cur.Less(latest) {
			return emit(updateJSON{Current: e.Build.Version, Latest: latest.String()}, fmt.Sprintf("outfit %s is up to date", cur))
		}
	}
	pkg, err := client.Fetch(ctx, want)
	if err != nil {
		return fail(asCLIError(err))
	}
	if !dev && want == nil && !cur.Less(pkg.Version) {
		return emit(updateJSON{Current: e.Build.Version, Latest: pkg.Version.String()}, fmt.Sprintf("outfit %s is up to date", cur))
	}
	path, err := update.Replace(e.Exe, pkg.Binary)
	if err != nil {
		return fail(asCLIError(err))
	}
	verb := "Updated"
	switch {
	case want != nil && !dev && *want == cur:
		verb = "Reinstalled"
	case want != nil && !dev && want.Less(cur):
		verb = "Downgraded"
	}
	return emit(updateJSON{Current: e.Build.Version, Latest: pkg.Version.String(), Updated: true, Installed: pkg.Version.String(), Path: path},
		fmt.Sprintf("%s outfit %s → %s (%s)\nverified: minisign signature and SHA-256 of %s", verb, e.Build.Version, pkg.Version, path, pkg.Archive))
}

// bannerEnabled is the §5 rule: only on a terminal, without --json, not in CI, not
// opted out.
func bannerEnabled(e Env, args []string) bool {
	// Under a git hook (OUTFIT_HOOK_ACTIVE, or the index git exports to hooks) a
	// command like `outfit status` must stay fast too.
	return e.IsTTY && !wantsJSON(args) && e.Getenv("CI") == "" && e.Getenv("OUTFIT_NO_UPDATE_CHECK") == "" &&
		e.Getenv("OUTFIT_HOOK_ACTIVE") == "" && e.Getenv("GIT_INDEX_FILE") == ""
}

// newBanner builds the banner for an eligible command. Everything that can be slow
// (the `gh auth token` lookup included) happens inside the check, which runs in a
// goroutine under an 800 ms budget.
func newBanner(e Env) *update.Banner {
	return &update.Banner{
		Current: e.Build.Version,
		Dir:     update.CacheDir(e.Getenv),
		Now:     time.Now,
		Check: func(ctx context.Context) (update.Version, error) {
			c, _ := updateClient(ctx, e)
			return c.Latest(ctx)
		},
	}
}
