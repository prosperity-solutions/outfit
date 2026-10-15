package cli

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/prosperity-solutions/outfit/skills"
)

// The skills are documentation an agent acts on, so a command or flag they name that the
// binary does not have is a bug that ships. This reads every `outfit <command> …` inside
// code in the topics and the pointer skill, and checks the command exists and takes the
// flags written next to it. (Prose is not checked: "outfit does" is not a command.)
func TestSkillsNameOnlyRealCommandsAndFlags(t *testing.T) {
	cmds := map[string]bool{"hook": true, "help": true}
	for _, m := range regexp.MustCompile(`(?m)^  ([a-z]+)[ \n]`).FindAllStringSubmatch(usage, -1) {
		cmds[m[1]] = true
	}
	if !cmds["skills"] || !cmds["sync"] || !cmds["author"] {
		t.Fatalf("could not read the command list from usage: %v", cmds)
	}
	flagsOf := func(cmd string) map[string]bool {
		var out, errb bytes.Buffer
		Execute(context.Background(), Env{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }}, []string{cmd, "-h"})
		m := map[string]bool{}
		for _, f := range regexp.MustCompile(`--([a-z-]+)`).FindAllStringSubmatch(out.String(), -1) {
			m[f[1]] = true
		}
		return m
	}

	texts := map[string]string{"outfit/SKILL.md": skills.Pointer}
	for _, tp := range skills.Topics() {
		texts["topics/"+tp.Name+".md"] = tp.Body
	}
	span := regexp.MustCompile("`([^`\n]+)`")
	cmdUse := regexp.MustCompile(`(?:^|[\s(|])outfit ([a-z][a-z-]*)\b([^|;&\n]*)`)
	flagUse := regexp.MustCompile(`--([a-z][a-z-]*)`)
	for name, text := range texts {
		var code []string
		inFence := false
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "```") {
				inFence = !inFence
				continue
			}
			if inFence {
				// Only the command, not the comment after it.
				code = append(code, strings.SplitN(line, " #", 2)[0])
				continue
			}
			for _, m := range span.FindAllStringSubmatch(line, -1) {
				code = append(code, m[1])
			}
		}
		for _, c := range code {
			for _, m := range cmdUse.FindAllStringSubmatch(c, -1) {
				cmd := m[1]
				if !cmds[cmd] {
					t.Errorf("%s: %q names `outfit %s`, which is not a command", name, c, cmd)
					continue
				}
				if cmd == "hook" || cmd == "help" {
					continue
				}
				known := flagsOf(cmd)
				for _, f := range flagUse.FindAllStringSubmatch(m[2], -1) {
					if !known[f[1]] {
						t.Errorf("%s: %q gives `outfit %s` the flag --%s, which it does not take", name, c, cmd, f[1])
					}
				}
			}
		}
	}
}
