// Package build turns a play_session_start into a Godot project directory
// that is exactly the requested commit, safe to run next to Connery's editor.
//
//  1. A bare mirror per repo (`git clone --mirror`, then `fetch --prune`),
//     authenticated with a read-only deploy key that lives in a file on the
//     host. Only repos named in the host config are ever cloned.
//  2. `git -C <mirror> worktree add --detach <root>/<task8>-<sha12> <sha>`.
//     The sha is the only ref git is ever given; the branch name core sends
//     is display text here and never reaches a git argv.
//  3. ASSERT `git -C <dir> rev-parse HEAD == sha` before anything runs.
//  4. Copy the suite_play addon in and register it as an autoload; in this
//     throwaway copy only, remove Voltron's `_mcp_game_helper` autoload and
//     the `godot_ai` editor plugin (see PatchProjectGodot for why both).
//  5. `godot --headless --import --path <dir>` under a timeout, reporting
//     progress every 5 s.
package build

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Repo is one repository the host is allowed to build.
type Repo struct {
	// Match is the normalized repo ("github.com/ryanmilvenan/voltron") that
	// core's repo_url must normalize to.
	Match string `json:"match"`
	// CloneURL is what git fetches from, e.g. git@github.com:owner/name.git.
	CloneURL string `json:"clone_url"`
	// SSHKey is the deploy key file (read-only key; file only).
	SSHKey string `json:"ssh_key"`
}

// Config is the builder's shape.
type Config struct {
	Git           string // git executable
	SSH           string // ssh executable for GIT_SSH_COMMAND (Git for Windows: usr/bin/ssh.exe)
	KnownHosts    string
	MirrorsDir    string
	CheckoutsDir  string
	AddonDir      string // source of addons/suite_play
	Repos         []Repo
	KeepCheckouts int // how many old checkouts to keep; 0 = 3
}

var (
	shaRe  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	uuidRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	safeRe = regexp.MustCompile(`[^a-z0-9]+`)
)

// NormalizeRepo reduces https, ssh and scp-style git URLs to host/owner/name.
func NormalizeRepo(raw string) (string, error) {
	s := strings.TrimSpace(strings.ToLower(raw))
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	switch {
	case strings.HasPrefix(s, "https://"), strings.HasPrefix(s, "http://"), strings.HasPrefix(s, "ssh://"):
		s = s[strings.Index(s, "://")+3:]
		if at := strings.Index(s, "@"); at >= 0 && at < strings.Index(s+"/", "/") {
			s = s[at+1:]
		}
		if slash := strings.Index(s, "/"); slash >= 0 {
			host := s[:slash]
			if c := strings.Index(host, ":"); c >= 0 {
				host = host[:c]
			}
			s = host + s[slash:]
		}
	case strings.Contains(s, "@") && strings.Contains(s, ":"):
		s = s[strings.Index(s, "@")+1:]
		s = strings.Replace(s, ":", "/", 1)
	}
	parts := strings.Split(s, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", fmt.Errorf("repo url %q is not host/owner/name", raw)
	}
	return s, nil
}

// FindRepo returns the configured repo core's repo_url names.
func (c Config) FindRepo(repoURL string) (Repo, error) {
	n, err := NormalizeRepo(repoURL)
	if err != nil {
		return Repo{}, err
	}
	for _, r := range c.Repos {
		if m, err := NormalizeRepo(r.Match); err == nil && m == n {
			return r, nil
		}
	}
	return Repo{}, fmt.Errorf("repo %s is not in this host's allowlist", n)
}

// Task8 is the checkout-name prefix: the first 8 hex of the task id, else of
// a UUID in the branch name, else of the session id.
func Task8(taskID, branch, sessionID string) string {
	for _, s := range []string{taskID, branch, sessionID} {
		if m := uuidRe.FindString(strings.ToLower(s)); m != "" {
			return m[:8]
		}
	}
	s := safeRe.ReplaceAllString(strings.ToLower(sessionID), "")
	if len(s) > 8 {
		s = s[:8]
	}
	if s == "" {
		s = "session"
	}
	return s
}

// CheckoutDir is <root>/<task8>-<sha12>.
func CheckoutDir(root, task8, sha string) (string, error) {
	if !shaRe.MatchString(sha) {
		return "", fmt.Errorf("sha %q is not 40 lowercase hex", sha)
	}
	if task8 == "" || safeRe.MatchString(task8) {
		return "", fmt.Errorf("bad checkout prefix %q", task8)
	}
	return filepath.Join(root, task8+"-"+sha[:12]), nil
}

// MirrorDir is <mirrors>/<owner>-<name>.git.
func (c Config) MirrorDir(r Repo) string {
	n, _ := NormalizeRepo(r.Match)
	parts := strings.Split(n, "/")
	return filepath.Join(c.MirrorsDir, safeRe.ReplaceAllString(parts[1]+"-"+parts[2], "-")+".git")
}

// Builder runs git.
type Builder struct {
	Cfg  Config
	Logf func(string, ...any)
}

func (b *Builder) git(ctx context.Context, r *Repo, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, b.Cfg.Git, args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_LFS_SKIP_SMUDGE=0")
	if r != nil && r.SSHKey != "" {
		ssh := b.Cfg.SSH
		if ssh == "" {
			ssh = "ssh"
		}
		kh := b.Cfg.KnownHosts
		sshCmd := fmt.Sprintf("'%s' -i '%s' -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new",
			filepath.ToSlash(ssh), filepath.ToSlash(r.SSHKey))
		if kh != "" {
			sshCmd += fmt.Sprintf(" -o UserKnownHostsFile='%s'", filepath.ToSlash(kh))
		}
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND="+sshCmd)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.Stdin = nil
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(lastLines(errb.String(), 4)))
	}
	return strings.TrimSpace(out.String()), nil
}

func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, " | ")
}

// EnsureCommit makes sure the mirror exists and holds sha.
func (b *Builder) EnsureCommit(ctx context.Context, r Repo, sha string) (string, error) {
	mirror := b.Cfg.MirrorDir(r)
	if _, err := os.Stat(filepath.Join(mirror, "HEAD")); err != nil {
		if err := os.MkdirAll(b.Cfg.MirrorsDir, 0o755); err != nil {
			return "", err
		}
		if _, err := b.git(ctx, &r, "clone", "--mirror", r.CloneURL, mirror); err != nil {
			return "", err
		}
	}
	if _, err := b.git(ctx, nil, "-C", mirror, "cat-file", "-e", sha+"^{commit}"); err == nil {
		return mirror, nil
	}
	if _, err := b.git(ctx, &r, "-C", mirror, "fetch", "--prune", "origin"); err != nil {
		return "", err
	}
	if _, err := b.git(ctx, nil, "-C", mirror, "cat-file", "-e", sha+"^{commit}"); err == nil {
		return mirror, nil
	}
	// A commit no ref points at (a force-pushed branch): ask for it by id.
	if _, err := b.git(ctx, &r, "-C", mirror, "fetch", "origin", sha); err != nil {
		return "", fmt.Errorf("commit %s is not on the remote: %w", sha, err)
	}
	return mirror, nil
}

// Checkout adds (or reuses) the worktree for sha and asserts HEAD == sha.
func (b *Builder) Checkout(ctx context.Context, r Repo, mirror, dir, sha string) error {
	if _, err := os.Stat(dir); err == nil {
		head, err := b.git(ctx, nil, "-C", dir, "rev-parse", "HEAD")
		if err == nil && head == sha {
			return nil
		}
		// Wrong or broken: remove it and add afresh.
		_, _ = b.git(ctx, nil, "-C", mirror, "worktree", "remove", "--force", dir)
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove stale checkout: %w", err)
		}
		_, _ = b.git(ctx, nil, "-C", mirror, "worktree", "prune")
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	if _, err := b.git(ctx, &r, "-C", mirror, "worktree", "add", "--detach", "--force", dir, sha); err != nil {
		return err
	}
	return b.AssertHead(ctx, dir, sha)
}

// ErrWrongHead means the checkout is not the requested commit.
var ErrWrongHead = errors.New("checkout HEAD is not the requested sha")

// AssertHead is the pre-launch guard: rev-parse HEAD must equal sha.
func (b *Builder) AssertHead(ctx context.Context, dir, sha string) error {
	head, err := b.git(ctx, nil, "-C", dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != sha {
		return fmt.Errorf("%w: HEAD %s, want %s", ErrWrongHead, head, sha)
	}
	return nil
}

// RemoveCheckout deletes one session checkout at the end of its session:
// `git worktree remove --force` on the bare mirror, then the directory
// itself (retried for a few seconds, because on Windows a Godot that was
// just terminated can hold its files open briefly), then `worktree prune`.
// The mirror is kept, so the next build of any sha in the repo is a local
// worktree add plus an asset import. dir must lie inside CheckoutsDir.
func (b *Builder) RemoveCheckout(ctx context.Context, mirror, dir string) error {
	root, err := filepath.Abs(b.Cfg.CheckoutsDir)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(root, abs); err != nil || rel == "." || strings.HasPrefix(rel, "..") || strings.ContainsAny(rel, `/\`) {
		return fmt.Errorf("refusing to remove %s: not a checkout directly under %s", dir, root)
	}
	_, _ = b.git(ctx, nil, "-C", mirror, "worktree", "remove", "--force", abs)
	var rmErr error
	for i := 0; i < 20; i++ {
		if rmErr = os.RemoveAll(abs); rmErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	_, _ = b.git(ctx, nil, "-C", mirror, "worktree", "prune")
	if rmErr != nil {
		return fmt.Errorf("remove checkout: %w", rmErr)
	}
	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("remove checkout: %s still exists", abs)
	}
	return nil
}

// Prune removes all but the newest keep checkouts (never `except`).
func (b *Builder) Prune(ctx context.Context, mirror, except string) {
	keep := b.Cfg.KeepCheckouts
	if keep <= 0 {
		keep = 3
	}
	ents, err := os.ReadDir(b.Cfg.CheckoutsDir)
	if err != nil {
		return
	}
	type ck struct {
		path string
		mod  time.Time
	}
	var cks []ck
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(b.Cfg.CheckoutsDir, e.Name())
		if p != except {
			cks = append(cks, ck{p, info.ModTime()})
		}
	}
	sort.Slice(cks, func(i, j int) bool { return cks[i].mod.After(cks[j].mod) })
	for i, c := range cks {
		if i < keep-1 {
			continue
		}
		_, _ = b.git(ctx, nil, "-C", mirror, "worktree", "remove", "--force", c.path)
		if b.Logf != nil {
			b.Logf("build: pruned old checkout %s", c.path)
		}
	}
	_, _ = b.git(ctx, nil, "-C", mirror, "worktree", "prune")
}

// InstallAddon copies AddonDir to <dir>/addons/suite_play and patches
// project.godot. It returns what the patch removed, for the host log.
func (b *Builder) InstallAddon(dir string) ([]string, error) {
	dst := filepath.Join(dir, "addons", "suite_play")
	if err := os.RemoveAll(dst); err != nil {
		return nil, err
	}
	if err := copyTree(b.Cfg.AddonDir, dst); err != nil {
		return nil, fmt.Errorf("copy addon: %w", err)
	}
	pg := filepath.Join(dir, "project.godot")
	src, err := os.ReadFile(pg)
	if err != nil {
		return nil, err
	}
	out, removed := PatchProjectGodot(string(src))
	return removed, os.WriteFile(pg, []byte(out), 0o644)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		t := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(t, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(t)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

// Autoload is the line the addon is registered with.
const Autoload = `SuitePlay="*res://addons/suite_play/suite_play.gd"`

var pluginEntryRe = regexp.MustCompile(`"[^"]*"`)

// PatchProjectGodot edits a project.godot for a played build:
//
//   - [autoload]: drop `_mcp_game_helper=` (Voltron's godot_ai game helper,
//     which answers the editor over the EngineDebugger channel) and add
//     SuitePlay, replacing any earlier SuitePlay line;
//   - [editor_plugins]: drop every res://addons/godot_ai/ entry. This one
//     matters for the IMPORT step: `godot --headless --import` runs the
//     editor, which loads enabled editor plugins, and godot_ai's editor
//     plugin dials ws://127.0.0.1:9500 (addons/godot_ai/connection.gd:347,
//     DEFAULT_WS_PORT in client_configurator.gd:41) — Connery's MCP backend.
//
// Everything else is left byte-for-byte. It returns the removed lines.
func PatchProjectGodot(src string) (string, []string) {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out, removed []string
	section := ""
	sawAutoload, added := false, false
	flushAutoload := func() {
		if section == "autoload" && !added {
			// Drop the section's trailing blanks, then: header, blank, entries,
			// SuitePlay, one blank — the layout the editor itself writes.
			for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
				out = out[:len(out)-1]
			}
			if strings.TrimSpace(out[len(out)-1]) == "[autoload]" {
				out = append(out, "")
			}
			out = append(out, Autoload, "")
			added = true
		}
	}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			flushAutoload()
			section = strings.Trim(t, "[]")
			if section == "autoload" {
				sawAutoload = true
			}
			out = append(out, l)
			continue
		}
		switch section {
		case "autoload":
			if strings.HasPrefix(t, "_mcp_game_helper=") || strings.HasPrefix(t, "SuitePlay=") {
				if strings.HasPrefix(t, "_mcp_game_helper=") {
					removed = append(removed, t)
				}
				continue
			}
		case "editor_plugins":
			if strings.HasPrefix(t, "enabled=") {
				var keep []string
				for _, e := range pluginEntryRe.FindAllString(t, -1) {
					if strings.Contains(e, "res://addons/godot_ai/") {
						removed = append(removed, "editor_plugins "+e)
						continue
					}
					keep = append(keep, e)
				}
				out = append(out, "enabled=PackedStringArray("+strings.Join(keep, ", ")+")")
				continue
			}
		}
		out = append(out, l)
	}
	flushAutoload()
	if !sawAutoload {
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		out = append(out, "", "[autoload]", "", Autoload, "")
	}
	return strings.Join(out, "\n"), removed
}
