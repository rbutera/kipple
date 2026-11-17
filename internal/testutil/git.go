package testutil

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitEpoch is the default commit time; commit i is gitEpoch + i minutes.
var gitEpoch = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// gitEnv isolates git from the developer's configuration: no global or system
// config (so no signing, hooks or credential helpers), HOME set to the fixture,
// fixed identity and dates.
func gitEnv(home string, when time.Time) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			continue
		}
		env = append(env, kv)
	}
	date := when.Format(time.RFC3339)
	return append(env,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=kipple fixture",
		"GIT_AUTHOR_EMAIL=fixture@kipple.invalid",
		"GIT_COMMITTER_NAME=kipple fixture",
		"GIT_COMMITTER_EMAIL=fixture@kipple.invalid",
		"GIT_AUTHOR_DATE="+date,
		"GIT_COMMITTER_DATE="+date,
	)
}

func runGit(home, dir string, when time.Time, args ...string) (string, error) {
	full := append([]string{
		"-c", "commit.gpgsign=false",
		"-c", "tag.gpgsign=false",
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "init.defaultBranch=main",
		"-c", "core.autocrlf=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = gitEnv(home, when)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// Git runs git in dir with the same isolated environment NewHome uses and
// fails the test on error. HOME for git is KIPPLE_HOME (the current fixture).
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	GuardRealHome(t)
	out, err := runGit(os.Getenv("KIPPLE_HOME"), dir, time.Now(), args...)
	if err != nil {
		t.Fatalf("testutil.Git: %v", err)
	}
	return out
}

func buildRepo(home string, r Repo) error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is not on PATH; repo fixtures need it")
	}
	dir, err := resolve(home, r.Path)
	if err != nil {
		return err
	}
	if r.Pushed && (!r.Upstream || len(r.Commits) == 0) {
		return errors.New("repo.Pushed needs repo.Upstream and at least one commit")
	}
	branch := r.Branch
	if branch == "" {
		branch = "main"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := runGit(home, dir, gitEpoch, "init", "-q", "-b", branch); err != nil {
		return err
	}

	n := 0
	commit := func(c Commit) error {
		when := c.Time
		if when.IsZero() {
			when = gitEpoch.Add(time.Duration(n) * time.Minute)
		}
		n++
		for _, f := range c.Files {
			f.Path = r.Path + "/" + f.Path
			if err := writeFile(home, f); err != nil {
				return err
			}
		}
		if _, err := runGit(home, dir, when, "add", "-A"); err != nil {
			return err
		}
		msg := c.Message
		if msg == "" {
			msg = fmt.Sprintf("commit %d", n)
		}
		_, err := runGit(home, dir, when, "commit", "-q", "--allow-empty", "-m", msg)
		return err
	}

	for _, c := range r.Commits {
		if err := commit(c); err != nil {
			return err
		}
	}

	if r.Upstream {
		up := UpstreamDir(home, r.Path)
		if err := os.MkdirAll(filepath.Dir(up), 0o755); err != nil {
			return err
		}
		if _, err := runGit(home, filepath.Dir(up), gitEpoch, "init", "-q", "--bare", "-b", branch, up); err != nil {
			return err
		}
		if _, err := runGit(home, dir, gitEpoch, "remote", "add", "origin", up); err != nil {
			return err
		}
		if r.Pushed {
			if _, err := runGit(home, dir, gitEpoch, "push", "-q", "-u", "origin", branch); err != nil {
				return err
			}
		}
	}

	for _, c := range r.LocalCommits {
		if err := commit(c); err != nil {
			return err
		}
	}
	return nil
}
