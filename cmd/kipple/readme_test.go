package main

import (
	"os"
	"strings"
	"testing"
)

// Spec scenario "Files present" (distribution).
func TestFilesPresent(t *testing.T) {
	for _, f := range []string{"LICENSE", "README.md", "SECURITY.md", "llms.txt", "CONTRIBUTING.md"} {
		if fi, err := os.Stat("../../" + f); err != nil || fi.Size() == 0 {
			t.Errorf("%s missing or empty: %v", f, err)
		}
	}
}

// agentBlock returns the first sh code block in the README's "For agents"
// section.
func agentBlock(t *testing.T, readme string) string {
	t.Helper()
	i := strings.Index(readme, "\n## For agents\n")
	if i < 0 {
		t.Fatal("README has no \"For agents\" section")
	}
	sec := readme[i:]
	start := strings.Index(sec, "```sh\n")
	if start < 0 {
		t.Fatal("no sh block in the agent section")
	}
	sec = sec[start+len("```sh\n"):]
	return sec[:strings.Index(sec, "```")]
}

// Spec scenario "Documented flow runs" (cli-agent-interface): every kipple
// command in the README's agent section is run, verbatim, by
// scripts/agent-flow-check.sh, which CI runs against a fixture and which
// checks each exit code. The section covers scan, inspect, subset apply and
// restore, the exit codes and the schema command, and llms.txt carries the
// same contract.
func TestDocumentedFlowRuns(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	script := readRepoFile(t, "scripts/agent-flow-check.sh")
	ci := readRepoFile(t, ".github/workflows/ci.yml")
	block := agentBlock(t, readme)
	n := 0
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "kipple ") {
			continue
		}
		n++
		if !strings.Contains(script, "step 0 '"+line+"'") {
			t.Errorf("README command not run with exit 0 by the script: %s", line)
		}
	}
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "ID=") || strings.HasPrefix(line, "IDS=") || strings.HasPrefix(line, "RECEIPT=") {
			if !strings.Contains(script, line) {
				t.Errorf("README variable not set the same way in the script: %s", line)
			}
		}
	}
	for _, want := range []string{"kipple scan --json", "kipple explain", "--only", "kipple clean --apply --json", "kipple history --json", "kipple restore"} {
		if !strings.Contains(block, want) {
			t.Errorf("agent flow lacks %q", want)
		}
	}
	if n < 7 {
		t.Fatalf("only %d kipple commands in the agent block", n)
	}
	for _, code := range []string{"step 4 ", "step 3 ", "step 2 ", "step 1 "} {
		if !strings.Contains(script, code) {
			t.Errorf("the script does not check an exit code with %q", code)
		}
	}
	mustContain(t, "README.md", readme,
		"| 0 | Success. |", "| 1 | Error.", "| 2 | Usage error", "| 3 | Nothing to do", "| 4 | Partial",
		"`kipple schema plan`", "schema_version",
	)
	mustContain(t, "ci.yml", ci, "scripts/agent-flow-check.sh")
	llms := readRepoFile(t, "llms.txt")
	mustContain(t, "llms.txt", llms,
		"kipple scan --json", "--only", "kipple clean --apply --json", "kipple history --json", "kipple restore",
		"Exit codes: 0 success, 1 error, 2 usage error, 3 nothing to do, 4 partial",
		"kipple schema plan|receipt|rules|result", "docs/plan-schema.md", "docs/safety.md",
		"--permanent is refused",
	)
}

// Spec scenario "Verify checksums" (distribution), README side: the
// documented cosign verify-blob command checks the signature and
// certificate goreleaser writes against this repository's release workflow
// identity, and the checksum check follows.
func TestReadmeVerifyCommand(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	mustContain(t, "README.md", readme,
		"cosign verify-blob",
		"--bundle checksums.txt.sigstore.json",
		"--certificate checksums.txt.pem",
		"--signature checksums.txt.sig",
		`--certificate-identity "https://github.com/rbutera/kipple/.github/workflows/release.yml@refs/tags/v$VERSION"`,
		"--certificate-oidc-issuer https://token.actions.githubusercontent.com",
		"shasum -a 256 --check --ignore-missing checksums.txt",
		"go install github.com/rbutera/kipple/cmd/kipple@latest",
		"kipple_${VERSION}_darwin_universal.tar.gz",
	)
	// The archive name the README downloads is the one goreleaser writes.
	gr := readRepoFile(t, ".goreleaser.yaml")
	mustContain(t, ".goreleaser.yaml", gr, "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ if eq .Arch \"all\" }}universal")
	// v0.1.0 has no permanent delete, and the README says so in the safety
	// section and the flag table.
	mustContain(t, "README.md", readme, "There is no permanent delete in\n  v0.1.0", "| `--permanent` | clean | **Refused in v0.1.0**")
}

// Task 4.4: CONTRIBUTING.md states the fixture-only rule and the add-a-rule
// steps, and every test and command those steps name exists; the issue
// templates and the PR template are in place, and the PR template asks
// whether any test touches the real home.
func TestContributing(t *testing.T) {
	c := readRepoFile(t, "CONTRIBUTING.md")
	mustContain(t, "CONTRIBUTING.md", c,
		"## The one rule: fixtures only", "testutil.NewHome", "testutil.GuardedMain",
		"## Adding a rule", "What", "IfRemoved", "rules.Validate", "TestCatalogueCount",
		"TestAddARuleSteps", "go test ./internal/rules -run TestGoldenRulesJSON -update",
		"command make check", "## Review process",
	)
	sources := map[string][]string{
		"internal/rules/rules_test.go":  {"func TestCatalogueCount(", "func TestGoldenRulesJSON(", `flag.Bool("update"`},
		"internal/scan/addrule_test.go": {"func TestAddARuleSteps("},
		"internal/rules/validate.go":    {"func Validate("},
		"internal/testutil/guard.go":    {"func GuardedMain("},
		"internal/testutil/home.go":     {"func NewHome("},
		"Makefile":                      {"\ncheck:", "agent-flow-check:", "release-snapshot:"},
		"internal/rules/catalogue.go":   {"var children = Match{Kind: MatchChildren}"},
	}
	for file, wants := range sources {
		text := readRepoFile(t, file)
		for _, w := range wants {
			if !strings.Contains(text, w) {
				t.Errorf("%s lacks %q, which CONTRIBUTING.md relies on", file, w)
			}
		}
	}
	escape := readRepoFile(t, ".github/ISSUE_TEMPLATE/path-escaped-allowlist.yml")
	mustContain(t, "path-escaped-allowlist.yml", escape, "name: Path escaped the allowlist", "security bug", "Report a vulnerability", "KIPPLE_HOME")
	rule := readRepoFile(t, ".github/ISSUE_TEMPLATE/new-rule.yml")
	mustContain(t, "new-rule.yml", rule, "name: New rule", "What it is", "What happens if it is removed")
	pr := readRepoFile(t, ".github/pull_request_template.md")
	mustContain(t, "pull_request_template.md", pr, "Does any test touch the real home?")
}
