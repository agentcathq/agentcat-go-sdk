package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The steps of .github/workflows/release.yml (scripts/release), as in the Java, Rust and Ruby SDKs.

const repo = "https://github.com/agentcathq/agentcat-go-sdk"

const changelogText = `# Changelog

Intro.

## [Unreleased]

### Added

- A feature.

## [2.0.0] - 2026-11-03

### Added

- The first release.

[Unreleased]: ` + repo + `/compare/v2.0.0...HEAD
[2.0.0]: ` + repo + `/releases/tag/v2.0.0
`

const adapterMod = `module go.agentcat.com/sdk/mcpgo/v2

go 1.25.5

require (
	github.com/mark3labs/mcp-go v0.57.0
	go.agentcat.com/sdk/v2 v2.0.0
)

require (
	go.agentcat.com/api v1.0.4 // indirect
	gopkg.in/validator.v2 v2.0.1 // indirect
)

replace go.agentcat.com/sdk/v2 => ../
`

const exampleMod = `module go.agentcat.com/sdk/examples/mcpgo/basic

go 1.25.5

require go.agentcat.com/sdk/mcpgo/v2 v2.0.0

require (
	go.agentcat.com/api v1.0.4 // indirect
	go.agentcat.com/sdk/v2 v2.0.0 // indirect
)

replace (
	go.agentcat.com/sdk/mcpgo/v2 => ../../../mcpgo
	go.agentcat.com/sdk/v2 => ../../../
)
`

func v(t *testing.T, text string) version {
	t.Helper()
	parsed, ok := parseVersion(text)
	if !ok {
		t.Fatalf("%q is not a version", text)
	}
	return parsed
}

func wantError(t *testing.T, err error, part string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want an error with %q, got none", part)
	}
	if !strings.Contains(err.Error(), part) {
		t.Fatalf("want an error with %q, got %q", part, err.Error())
	}
}

func TestVersionsParseOnlyAsMajorMinorPatch(t *testing.T) {
	if got := v(t, "1.20.3").String(); got != "1.20.3" {
		t.Errorf("got %s", got)
	}
	if got := v(t, "0.0.0").String(); got != "0.0.0" {
		t.Errorf("got %s", got)
	}
	for _, bad := range []string{"", "1", "1.2", "1.2.3.4", "01.2.3", "1.2.x", "v1.2.3", "1.2.3-rc.1", "1..3", "1.2.3\n"} {
		if _, ok := parseVersion(bad); ok {
			t.Errorf("%q parsed as a version", bad)
		}
	}
}

func TestTheNewestTagIgnoresTagsThatAreNotVersions(t *testing.T) {
	tags := []string{"v2.0.0", "v2.10.0", "v2.9.9", "attempt-1", "v3.0", "2.99.0", "mcpgo/v9.0.0", "officialsdk/v9.0.0", "v2.11.0-beta.1"}
	newest, ok := newestTag(tags)
	if !ok || newest != v(t, "2.10.0") {
		t.Errorf("got %s %v", newest, ok)
	}
	if _, ok := newestTag([]string{"attempt-1", "mcpgo/v2.1.0"}); ok {
		t.Error("want no tag")
	}
}

func TestWordsCountFromTheNewestTag(t *testing.T) {
	newest := v(t, "2.2.3")
	for input, want := range map[string]string{"patch": "2.2.4", "minor": "2.3.0", "2.5.0": "2.5.0"} {
		got, err := nextVersion(input, newest, newest, true)
		if err != nil || got != v(t, want) {
			t.Errorf("%s: got %s %v, want %s", input, got, err, want)
		}
	}
}

func TestAMajorVersionNeedsNewModulePaths(t *testing.T) {
	newest := v(t, "2.2.3")
	_, err := nextVersion("major", newest, newest, true)
	wantError(t, err, "3.0.0 needs the module paths /v3, not /v2")
	_, err = nextVersion("3.1.0", newest, newest, true)
	wantError(t, err, "needs the module paths /v3")
}

func TestAnExactVersionMustBeNewerThanTheNewestTag(t *testing.T) {
	newest := v(t, "2.2.3")
	_, err := nextVersion("2.2.3", newest, newest, true)
	if err == nil || err.Error() != "2.2.3 is not newer than the newest tag, v2.2.3" {
		t.Errorf("got %v", err)
	}
	_, err = nextVersion("2.0.9", newest, newest, true)
	wantError(t, err, "is not newer")
	_, err = nextVersion("next", newest, newest, true)
	if err == nil || err.Error() != `"next" is not patch, minor, major or a version X.Y.Z` {
		t.Errorf("got %v", err)
	}
}

func TestTheFirstReleaseIsTheVersionOfTheGoModFiles(t *testing.T) {
	for _, word := range []string{"patch", "minor", "major", "2.0.0"} {
		got, err := nextVersion(word, v(t, "2.0.0"), version{}, false)
		if err != nil || got != v(t, "2.0.0") {
			t.Errorf("%s: got %s %v", word, got, err)
		}
	}
	_, err := nextVersion("2.1.0", v(t, "2.0.0"), version{}, false)
	wantError(t, err, "the first release is the version of mcpgo/go.mod, 2.0.0")
	_, err = nextVersion("next", v(t, "2.0.0"), version{}, false)
	wantError(t, err, "is not patch")
}

func TestEveryPlaceGetsTheNewVersion(t *testing.T) {
	got := setVersion("mcpgo/go.mod", adapterMod, v(t, "2.0.1"))
	want := strings.Replace(adapterMod, "go.agentcat.com/sdk/v2 v2.0.0", "go.agentcat.com/sdk/v2 v2.0.1", 1)
	if got != want {
		t.Errorf("adapter go.mod:\n%s", got)
	}
	// Both requires change; the replace lines and other modules keep their text.
	got = setVersion("examples/mcpgo/basic/go.mod", exampleMod, v(t, "2.0.1"))
	if want := strings.ReplaceAll(exampleMod, " v2.0.0", " v2.0.1"); got != want {
		t.Errorf("example go.mod:\n%s", got)
	}
	readme := "go get go.agentcat.com/sdk/mcpgo/v2@v2.0.0\ngo get go.agentcat.com/sdk/officialsdk/v2\n`go.agentcat.com/sdk/v2@v2.0.0`\n"
	if got, want := setVersion("README.md", readme, v(t, "2.0.1")), strings.ReplaceAll(readme, "@v2.0.0", "@v2.0.1"); got != want {
		t.Errorf("README.md:\n%s", got)
	}
}

func TestThePlacesMustAgree(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":                      "module go.agentcat.com/sdk/v2\n",
		"mcpgo/go.mod":                adapterMod,
		"officialsdk/go.mod":          strings.Replace(adapterMod, "mcpgo", "officialsdk", 1),
		"examples/mcpgo/basic/go.mod": exampleMod,
		"README.md":                   "go get go.agentcat.com/sdk/mcpgo/v2\n",
		".github/x/go.mod":            "require go.agentcat.com/sdk/v2 v9.9.9\n",
		"testdata/go.mod":             "require go.agentcat.com/sdk/v2 v9.9.9\n",
	})
	places, err := findPlaces(dir)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, p := range places {
		found = append(found, p.String())
	}
	want := []string{
		"examples/mcpgo/basic/go.mod:5 go.agentcat.com/sdk/mcpgo/v2 v2.0.0",
		"examples/mcpgo/basic/go.mod:9 go.agentcat.com/sdk/v2 v2.0.0",
		"mcpgo/go.mod:7 go.agentcat.com/sdk/v2 v2.0.0",
		"officialsdk/go.mod:7 go.agentcat.com/sdk/v2 v2.0.0",
	}
	if strings.Join(found, "\n") != strings.Join(want, "\n") {
		t.Fatalf("places:\n%s", strings.Join(found, "\n"))
	}
	if got, err := currentVersion(places); err != nil || got != v(t, "2.0.0") {
		t.Errorf("got %s %v", got, err)
	}

	// One example on another version stops every step.
	writeFiles(t, dir, map[string]string{"README.md": "go get go.agentcat.com/sdk/mcpgo/v2@v2.0.1\n"})
	places, err = findPlaces(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = currentVersion(places)
	wantError(t, err, "the places that hold the version do not agree (README.md has v2.0.1, examples/mcpgo/basic/go.mod has v2.0.0)")
	wantError(t, err, "  README.md:1 go.agentcat.com/sdk/mcpgo/v2 v2.0.1")

	// A pre-release is not a version of this flow.
	_, err = currentVersion([]place{{"mcpgo/go.mod", 7, rootModule, "v2.0.0-beta.1"}, {"officialsdk/go.mod", 7, rootModule, "v2.0.0-beta.1"}})
	wantError(t, err, "is not a version vX.Y.Z")

	// An adapter that does not require the root module has no version.
	_, err = currentVersion([]place{{"mcpgo/go.mod", 7, rootModule, "v2.0.0"}})
	wantError(t, err, "officialsdk/go.mod has no `require go.agentcat.com/sdk/v2 vX.Y.Z` line")
}

func TestDatingMovesTheEntriesUnderTheVersionAndLinksBothSections(t *testing.T) {
	dated, err := dateChangelog(changelogText, v(t, "2.1.0"), "2026-12-01", v(t, "2.0.0"), true)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(changelogText, "## [Unreleased]\n", "## [Unreleased]\n\n## [2.1.0] - 2026-12-01\n", 1)
	want = strings.Replace(want, "[Unreleased]: "+repo+"/compare/v2.0.0...HEAD\n",
		"[Unreleased]: "+repo+"/compare/v2.1.0...HEAD\n[2.1.0]: "+repo+"/compare/v2.0.0...v2.1.0\n", 1)
	if dated != want {
		t.Errorf("dated:\n%s", dated)
	}
	date, body, ok := datedSection(dated, v(t, "2.1.0"))
	if !ok || date != "2026-12-01" || body != "### Added\n\n- A feature." {
		t.Errorf("got %q %q %v", date, body, ok)
	}
}

func TestTheFirstReleaseLinksItsTag(t *testing.T) {
	first := "## [Unreleased]\n\n- One.\n\n[Unreleased]: " + repo + "/commits/main\n"
	got, err := dateChangelog(first, v(t, "2.0.0"), "2026-11-03", version{}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "## [Unreleased]\n\n## [2.0.0] - 2026-11-03\n\n- One.\n\n" +
		"[Unreleased]: " + repo + "/compare/v2.0.0...HEAD\n[2.0.0]: " + repo + "/releases/tag/v2.0.0\n"
	if got != want {
		t.Errorf("got:\n%s", got)
	}
}

func TestDatingRefusesAnEmptySectionABadDateAndAMissingSection(t *testing.T) {
	empty := "## [Unreleased]\n\n### Added\n\n## [2.0.0] - 2026-11-03\n\n- One.\n\n[Unreleased]: x\n"
	_, err := dateChangelog(empty, v(t, "2.0.1"), "2026-12-01", v(t, "2.0.0"), true)
	wantError(t, err, "has no entry")
	_, err = dateChangelog(changelogText, v(t, "2.1.0"), "2026-12-1", version{}, false)
	wantError(t, err, "is not a date YYYY-MM-DD")
	_, err = dateChangelog("# Changelog\n", v(t, "2.1.0"), "2026-12-01", version{}, false)
	wantError(t, err, "has no ## [Unreleased] section")
	_, err = dateChangelog("## [Unreleased]\n\n- One.\n", v(t, "2.0.0"), "2026-12-01", version{}, false)
	wantError(t, err, "no [Unreleased] link")
}

func TestASectionWithoutAValidDateIsNotDated(t *testing.T) {
	if date, _, ok := datedSection(changelogText, v(t, "2.0.0")); !ok || date != "2026-11-03" {
		t.Errorf("got %q %v", date, ok)
	}
	if _, _, ok := datedSection("## [2.0.0] - soon\n- One.\n", v(t, "2.0.0")); ok {
		t.Error("an undated section counts as dated")
	}
	if _, _, ok := datedSection(changelogText, v(t, "9.9.9")); ok {
		t.Error("a missing section counts as dated")
	}
}

func TestRunRejectsUnknownArgumentsWithTheUsage(t *testing.T) {
	code, text := run([]string{"publish"}, ".")
	if code != 2 || !strings.HasPrefix(text, "usage: ") {
		t.Errorf("got %d %q", code, text)
	}
}

func TestRunRefusesADirectoryThatIsNotTheRepository(t *testing.T) {
	code, text := run([]string{"status"}, t.TempDir())
	if code != 1 || !strings.Contains(text, "run the release steps from the root of the repository") {
		t.Errorf("got %d %q", code, text)
	}
}

// The three steps on a git repository, as the workflow runs them.
func TestPrepareStatusAndNotesOnARepository(t *testing.T) {
	dir := gitRepository(t, fakeFiles("2.0.0", changelogText), "v2.0.0", "mcpgo/v2.0.0", "officialsdk/v2.0.0")
	expect(t, dir, []string{"status"}, 0, "2.0.0 skip")

	expect(t, dir, []string{"prepare", "patch", "2026-12-01"}, 0, "2.0.1")
	for _, file := range []string{"mcpgo/go.mod", "officialsdk/go.mod", "examples/mcpgo/basic/go.mod"} {
		text := readFile(t, dir, file)
		if strings.Contains(text, "v2.0.0") || !strings.Contains(text, "go.agentcat.com/sdk/v2 v2.0.1") {
			t.Errorf("%s:\n%s", file, text)
		}
	}
	if text := readFile(t, dir, "README.md"); !strings.Contains(text, "mcpgo/v2@v2.0.1") {
		t.Errorf("README.md:\n%s", text)
	}
	expect(t, dir, []string{"status"}, 0, "2.0.1 publish")
	expect(t, dir, []string{"notes", "2.0.1"}, 0, "### Added\n\n- A feature.")

	code, text := run([]string{"prepare", "patch", "2026-12-02"}, dir)
	if code != 1 || !strings.Contains(text, "has no entry") {
		t.Errorf("got %d %q", code, text)
	}
}

// A release that stopped between its tags is still to publish; with all three tags it is done.
func TestStatusPublishesUntilAllThreeTagsExist(t *testing.T) {
	dated, err := dateChangelog(changelogText, v(t, "2.0.1"), "2026-12-01", v(t, "2.0.0"), true)
	if err != nil {
		t.Fatal(err)
	}
	dir := gitRepository(t, fakeFiles("2.0.1", dated), "v2.0.0", "mcpgo/v2.0.0", "officialsdk/v2.0.0")
	expect(t, dir, []string{"status"}, 0, "2.0.1 publish")
	git(t, dir, "tag", "v2.0.1")
	expect(t, dir, []string{"status"}, 0, "2.0.1 publish")
	git(t, dir, "tag", "mcpgo/v2.0.1")
	expect(t, dir, []string{"status"}, 0, "2.0.1 publish")
	git(t, dir, "tag", "officialsdk/v2.0.1")
	expect(t, dir, []string{"status"}, 0, "2.0.1 skip")
}

// The places of this repository agree, and its CHANGELOG can always release their version.
// Before the release pull request, `prepare` dates the [Unreleased] entries; after it, the
// version has its dated section, so `status` publishes it while it has no tags.
func TestThisRepositoryCanReleaseTheVersionOfTheGoModFiles(t *testing.T) {
	root := filepath.Join("..", "..")
	places, err := findPlaces(root)
	if err != nil {
		t.Fatal(err)
	}
	current, err := currentVersion(places)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"go.mod": readFile(t, root, "go.mod"), changelog: readFile(t, root, changelog)}
	for _, p := range places {
		files[p.file] = readFile(t, root, p.file)
	}
	dir := gitRepository(t, files)
	if _, _, ok := datedSection(files[changelog], current); !ok {
		expect(t, dir, []string{"prepare", "patch", "2026-11-10"}, 0, current.String())
	}
	expect(t, dir, []string{"status"}, 0, current.String()+" publish")

	code, notes := run([]string{"notes", current.String()}, dir)
	if code != 0 || !strings.Contains(notes, "### ") || strings.Contains(notes, "## [") {
		t.Errorf("got %d %q", code, notes)
	}
	if text := readFile(t, dir, changelog); !strings.Contains(text, "["+current.String()+"]: "+repo+"/") {
		t.Errorf("no link for %s:\n%s", current, text)
	}
}

func fakeFiles(ver, changelog string) map[string]string {
	set := func(text string) string { return strings.ReplaceAll(text, " v2.0.0", " v"+ver) }
	return map[string]string{
		"go.mod":                      "module go.agentcat.com/sdk/v2\n\ngo 1.24.0\n",
		"mcpgo/go.mod":                set(adapterMod),
		"officialsdk/go.mod":          set(strings.Replace(adapterMod, "mcpgo", "officialsdk", 1)),
		"examples/mcpgo/basic/go.mod": set(exampleMod),
		"README.md":                   "go get go.agentcat.com/sdk/mcpgo/v2@v" + ver + "\n",
		"CHANGELOG.md":                changelog,
	}
}

func gitRepository(t *testing.T, files map[string]string, tags ...string) string {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, files)
	git(t, dir, "init", "-q")
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "start")
	for _, tag := range tags {
		git(t, dir, "tag", tag)
	}
	return dir
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func expect(t *testing.T, dir string, args []string, code int, text string) {
	t.Helper()
	gotCode, gotText := run(args, dir)
	if gotCode != code || gotText != text {
		t.Errorf("%v: got %d %q, want %d %q", args, gotCode, gotText, code, text)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// The global and system git config of a machine can sign or annotate tags; the tests need
	// plain lightweight tags, as the release workflow creates them.
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
