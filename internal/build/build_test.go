package build

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// voltronProject is Voltron's project.godot at bebe173f (2026-09-26).
const voltronProject = `; Engine configuration file.
config_version=5

[application]

config/name="Voltron"
run/main_scene="res://main/main.tscn"
config/features=PackedStringArray("4.7", "Forward Plus")

[autoload]

_mcp_game_helper="*res://addons/godot_ai/runtime/game_helper.gd"

[editor_plugins]

enabled=PackedStringArray("res://addons/godot_ai/plugin.cfg")

[physics]

3d/physics_engine="Jolt Physics"
`

func TestPatchProjectGodotVoltron(t *testing.T) {
	out, removed := PatchProjectGodot(voltronProject)
	if strings.Contains(out, "_mcp_game_helper") || strings.Contains(out, "godot_ai") {
		t.Fatalf("godot_ai survived:\n%s", out)
	}
	if strings.Count(out, Autoload) != 1 {
		t.Fatalf("autoload not added once:\n%s", out)
	}
	if !strings.Contains(out, "[autoload]\n\n"+Autoload+"\n\n[editor_plugins]") {
		t.Fatalf("autoload placement:\n%s", out)
	}
	if !strings.Contains(out, "enabled=PackedStringArray()") {
		t.Fatalf("editor plugin list:\n%s", out)
	}
	for _, keep := range []string{`run/main_scene="res://main/main.tscn"`, `3d/physics_engine="Jolt Physics"`, "config_version=5"} {
		if !strings.Contains(out, keep) {
			t.Errorf("lost %q", keep)
		}
	}
	if len(removed) != 2 {
		t.Fatalf("removed %v", removed)
	}
	// Idempotent: patching twice changes nothing.
	again, removed2 := PatchProjectGodot(out)
	if again != out || len(removed2) != 0 {
		t.Fatalf("not idempotent:\n%s\n---\n%s", out, again)
	}
}

func TestPatchProjectGodotKeepsOtherPluginsAndAddsSection(t *testing.T) {
	src := "config_version=5\r\n\r\n[editor_plugins]\r\n\r\nenabled=PackedStringArray(\"res://addons/other/plugin.cfg\", \"res://addons/godot_ai/plugin.cfg\")\r\n"
	out, removed := PatchProjectGodot(src)
	if !strings.Contains(out, `enabled=PackedStringArray("res://addons/other/plugin.cfg")`) || len(removed) != 1 {
		t.Fatalf("out %s removed %v", out, removed)
	}
	if !strings.HasSuffix(out, "[autoload]\n\n"+Autoload+"\n") {
		t.Fatalf("no autoload section appended:\n%q", out)
	}
}

func TestNormalizeAndFindRepo(t *testing.T) {
	for _, u := range []string{
		"https://github.com/ryanmilvenan/voltron", "https://github.com/RyanMilvenan/voltron.git",
		"git@github.com:ryanmilvenan/voltron.git", "ssh://git@github.com/ryanmilvenan/voltron.git",
		"https://x-token@github.com/ryanmilvenan/voltron/", "ssh://git@github.com:22/ryanmilvenan/voltron",
	} {
		n, err := NormalizeRepo(u)
		if err != nil || n != "github.com/ryanmilvenan/voltron" {
			t.Errorf("%s -> %q %v", u, n, err)
		}
	}
	for _, bad := range []string{"", "voltron", "https://github.com/ryanmilvenan", "https://github.com/a/b/c"} {
		if _, err := NormalizeRepo(bad); err == nil {
			t.Errorf("%q normalized", bad)
		}
	}
	c := Config{Repos: []Repo{{Match: "github.com/ryanmilvenan/voltron", CloneURL: "git@github.com:ryanmilvenan/voltron.git"}}}
	if r, err := c.FindRepo("https://github.com/ryanmilvenan/voltron"); err != nil || r.CloneURL == "" {
		t.Fatal(r, err)
	}
	if _, err := c.FindRepo("https://github.com/evil/voltron"); err == nil {
		t.Fatal("unlisted repo accepted")
	}
}

func TestCheckoutNaming(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	if got := Task8("01a0db5f-4f1a-7001-87a3-e777ad1cab82", "", "s"); got != "01a0db5f" {
		t.Fatal(got)
	}
	if got := Task8("", "task/01a0dbd6-76bb-7001-a322-08a81e3d7927", "s"); got != "01a0dbd6" {
		t.Fatal(got)
	}
	if got := Task8("", "main", "Sess-1234-5678-xyz"); got != "sess1234" {
		t.Fatal(got)
	}
	d, err := CheckoutDir(filepath.FromSlash("/c/checkouts"), "01a0db5f", sha)
	if err != nil || d != filepath.Join(filepath.FromSlash("/c/checkouts"), "01a0db5f-0123456789ab") {
		t.Fatal(d, err)
	}
	for _, bad := range []struct{ t8, sha string }{{"01a0db5f", "abc"}, {"../x", sha}, {"", sha}, {"01a0db5f", strings.ToUpper(sha)}} {
		if _, err := CheckoutDir("/c", bad.t8, bad.sha); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func testGit(t *testing.T) string {
	t.Helper()
	if g := os.Getenv("PLAY_HOST_TEST_GIT"); g != "" {
		return g
	}
	g, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	return g
}

func run(t *testing.T, git, dir string, args ...string) string {
	t.Helper()
	c := exec.Command(git, append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestMirrorWorktreeExactSha builds a real upstream with two commits on a
// branch, then checks the builder checks out the OLDER sha it was asked for
// (never the branch head), asserts HEAD, reuses the checkout, and refuses a
// checkout that has drifted.
func TestMirrorWorktreeExactSha(t *testing.T) {
	git := testGit(t)
	root := t.TempDir()
	up := filepath.Join(root, "upstream")
	os.MkdirAll(up, 0o755)
	run(t, git, up, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(up, "project.godot"), []byte(voltronProject), 0o644)
	run(t, git, up, "add", ".")
	run(t, git, up, "commit", "-q", "-m", "one")
	want := run(t, git, up, "rev-parse", "HEAD")
	os.WriteFile(filepath.Join(up, "later.txt"), []byte("2"), 0o644)
	run(t, git, up, "add", ".")
	run(t, git, up, "commit", "-q", "-m", "two")
	head := run(t, git, up, "rev-parse", "HEAD")

	addon := filepath.Join(root, "addon")
	os.MkdirAll(addon, 0o755)
	os.WriteFile(filepath.Join(addon, "suite_play.gd"), []byte("extends Node\n"), 0o644)

	b := &Builder{Cfg: Config{
		Git: git, MirrorsDir: filepath.Join(root, "mirrors"), CheckoutsDir: filepath.Join(root, "checkouts"), AddonDir: addon,
		Repos: []Repo{{Match: "github.com/o/voltron", CloneURL: up}},
	}}
	r, _ := b.Cfg.FindRepo("https://github.com/o/voltron")
	ctx := context.Background()
	mirror, err := b.EnsureCommit(ctx, r, want)
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := CheckoutDir(b.Cfg.CheckoutsDir, "01a0db5f", want)
	if err := b.Checkout(ctx, r, mirror, dir, want); err != nil {
		t.Fatal(err)
	}
	if got := run(t, git, dir, "rev-parse", "HEAD"); got != want || got == head {
		t.Fatalf("checked out %s, want %s (branch head %s)", got, want, head)
	}
	if _, err := os.Stat(filepath.Join(dir, "later.txt")); err == nil {
		t.Fatal("checkout carries the later commit")
	}
	removed, err := b.InstallAddon(dir)
	if err != nil || len(removed) != 2 {
		t.Fatal(removed, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "addons", "suite_play", "suite_play.gd")); err != nil {
		t.Fatal(err)
	}
	// Re-checkout of the same sha reuses the dir (keeps the import cache).
	os.WriteFile(filepath.Join(dir, "marker"), nil, 0o644)
	if err := b.Checkout(ctx, r, mirror, dir, want); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "marker")); err != nil {
		t.Fatal("same-sha checkout was not reused")
	}
	// Drift: someone moves HEAD; the assertion must fail.
	run(t, git, dir, "checkout", "-q", "--detach", head)
	if err := b.AssertHead(ctx, dir, want); !errors.Is(err, ErrWrongHead) {
		t.Fatalf("drifted checkout passed: %v", err)
	}
	// And Checkout repairs it back to the exact sha.
	if err := b.Checkout(ctx, r, mirror, dir, want); err != nil {
		t.Fatal(err)
	}
	if got := run(t, git, dir, "rev-parse", "HEAD"); got != want {
		t.Fatalf("repair left HEAD at %s", got)
	}
	// A sha the remote has never had fails loudly, no fallback.
	if _, err := b.EnsureCommit(ctx, r, strings.Repeat("d", 40)); err == nil {
		t.Fatal("unknown sha accepted")
	}
	// A commit pushed after the mirror was made is fetched.
	os.WriteFile(filepath.Join(up, "three.txt"), []byte("3"), 0o644)
	run(t, git, up, "add", ".")
	run(t, git, up, "commit", "-q", "-m", "three")
	third := run(t, git, up, "rev-parse", "HEAD")
	if _, err := b.EnsureCommit(ctx, r, third); err != nil {
		t.Fatal(err)
	}
}

func TestImportArgsCarryMarker(t *testing.T) {
	a := strings.Join(ImportArgs("C:/co/x", "sess"), " ")
	if a != "--headless --import --path C:/co/x -- --suite-play-session=sess" {
		t.Fatal(a)
	}
}
