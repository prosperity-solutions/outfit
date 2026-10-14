// Package e2e runs the testscript scenarios in testdata/script against the real
// outfit command.
package e2e

import (
	"os"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/prosperity-solutions/outfit/internal/cli"
	"github.com/prosperity-solutions/outfit/internal/testkit"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"outfit": func() {
			os.Exit(cli.Main(cli.Build{Version: "v0.0.0-test", Commit: "test", Date: "test"}))
		},
	})
}

func TestScripts(t *testing.T) {
	testkit.SkipIfShort(t)
	testscript.Run(t, testscript.Params{
		Dir:       "../../testdata/script",
		Setup:     testkit.ScriptSetup,
		Cmds:      testkit.ScriptCommands(),
		Condition: testkit.ScriptCondition,
	})
}
