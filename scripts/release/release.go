// Command release holds the release steps of .github/workflows/release.yml, on the go.mod files
// that hold the version, CHANGELOG.md and the tags of the repository. The Java SDK
// (scripts/Release.java), the Rust SDK (xtask release) and the Ruby SDK (scripts/release.rb) have
// the same three steps. Run it from the root of the repository:
//
//	go run ./scripts/release prepare <patch | minor | major | X.Y.Z> <YYYY-MM-DD>
//	go run ./scripts/release status
//	go run ./scripts/release notes <X.Y.Z>
//
// The three modules (the root, mcpgo and officialsdk) ship together with one version. A version
// is a set of three git tags on one commit: vX.Y.Z, mcpgo/vX.Y.Z and officialsdk/vX.Y.Z. The
// version of the repository is the version that the adapters require of the root module
// (`go.agentcat.com/sdk/v2 vX.Y.Z` in mcpgo/go.mod and officialsdk/go.mod). Every other place
// that names one of the three modules with a version must agree with it: the requires of the
// examples, and `@vX.Y.Z` in README.md and MIGRATION.md. When they do not agree, each step
// stops.
//
//   - `prepare` picks the next version, writes it into every place that holds the version, turns
//     the [Unreleased] section of the CHANGELOG into the section of that version with the date,
//     and prints the version. `patch`, `minor` and `major` count from the newest `vX.Y.Z` tag.
//     With no tag yet, the first release is the version that the go.mod files hold, whatever the
//     input word. An exact version must be newer than the newest tag. A new major version needs
//     new module paths, so `prepare` refuses it. An [Unreleased] section with no entry stops the
//     step: a release needs a change.
//   - `status` prints `<version> publish` when the CHANGELOG has the dated section of the version
//     and at least one of its three tags is missing, else `<version> skip`. A release that
//     stopped after the first tag is still `publish`, so a new run completes it.
//   - `notes` prints the body of the CHANGELOG section of that version.
//
// The command lives in the root module so that `go test ./...` in CI tests it, also on Go 1.24.
// It uses only the standard library and exports nothing.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	repository = "https://github.com/agentcathq/agentcat-go-sdk"
	rootModule = "go.agentcat.com/sdk/v2"
	changelog  = "CHANGELOG.md"
	unreleased = "## [Unreleased]"
	usage      = "usage: go run ./scripts/release prepare <patch | minor | major | X.Y.Z> <YYYY-MM-DD> | status | notes <X.Y.Z>"

	// The major version of the module paths (/v2). A version with another major needs new paths.
	moduleMajor = 2
)

// versionFiles hold the version of the repository: the require of the root module.
var versionFiles = []string{"mcpgo/go.mod", "officialsdk/go.mod"}

// docFiles can hold install commands with a version (`go get <module>@vX.Y.Z`).
var docFiles = []string{"README.md", "MIGRATION.md"}

// tagPrefixes are the tag prefixes of the three modules, the root first: the order of the tags.
var tagPrefixes = []string{"v", "mcpgo/v", "officialsdk/v"}

var (
	// MAJOR.MINOR.PATCH with decimal numbers and no leading zero: no pre-release or build part.
	versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	datePattern    = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	// A require of one of the three modules in a go.mod file, in a require block or on one line.
	// A `replace` line has `=>` after the path, so it does not match.
	requireLine = regexp.MustCompile(`^(\s*(?:require\s+)?)(go\.agentcat\.com/sdk(?:/mcpgo|/officialsdk)?/v2)(\s+)(v\S+)(.*)$`)
	// An install command with a version in a Markdown file.
	docVersion = regexp.MustCompile(`(go\.agentcat\.com/sdk(?:/mcpgo|/officialsdk)?/v2)@(v[0-9][^\s` + "`" + `"')]*)`)
	moduleLine = regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`)
	linkLine   = regexp.MustCompile(`^\[[^\]]+\]: `)
)

type releaseError struct{ msg string }

func (e *releaseError) Error() string { return e.msg }

func fail(format string, args ...any) error {
	return &releaseError{fmt.Sprintf(format, args...)}
}

type version struct{ major, minor, patch int }

func parseVersion(text string) (version, bool) {
	m := versionPattern.FindStringSubmatch(text)
	if m == nil {
		return version{}, false
	}
	var parts [3]int
	for i := range parts {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return version{}, false
		}
		parts[i] = n
	}
	return version{parts[0], parts[1], parts[2]}, true
}

func (v version) String() string { return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch) }

func (v version) less(o version) bool {
	if v.major != o.major {
		return v.major < o.major
	}
	if v.minor != o.minor {
		return v.minor < o.minor
	}
	return v.patch < o.patch
}

// newestTag returns the newest of the `v<version>` tags; other tags (pre-releases, the tags of
// the adapter modules) do not count.
func newestTag(tags []string) (version, bool) {
	var newest version
	found := false
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "v") {
			continue
		}
		v, ok := parseVersion(tag[1:])
		if ok && (!found || newest.less(v)) {
			newest, found = v, true
		}
	}
	return newest, found
}

// nextVersion returns the version of the release that input asks for: see the package comment.
func nextVersion(input string, current version, newest version, hasTag bool) (version, error) {
	word := input == "patch" || input == "minor" || input == "major"
	exact, isExact := parseVersion(input)
	if !word && !isExact {
		return version{}, fail("%q is not patch, minor, major or a version X.Y.Z", input)
	}
	var next version
	switch {
	case !hasTag:
		if isExact && exact != current {
			return version{}, fail("the first release is the version of %s, %s; set it there to release %s", versionFiles[0], current, exact)
		}
		next = current
	case input == "patch":
		next = version{newest.major, newest.minor, newest.patch + 1}
	case input == "minor":
		next = version{newest.major, newest.minor + 1, 0}
	case input == "major":
		next = version{newest.major + 1, 0, 0}
	default:
		next = exact
	}
	if hasTag && !newest.less(next) {
		return version{}, fail("%s is not newer than the newest tag, v%s", next, newest)
	}
	if next.major != moduleMajor {
		return version{}, fail("%s needs the module paths /v%d, not /v%d; release a new major version by hand", next, next.major, moduleMajor)
	}
	return next, nil
}

// place is one line of a file that names one of the three modules with a version.
type place struct {
	file    string // relative to the root, with slashes
	line    int    // 1-based
	module  string
	version string // as written, with the leading v
}

func (p place) String() string {
	return fmt.Sprintf("%s:%d %s %s", p.file, p.line, p.module, p.version)
}

// findPlaces returns every place that holds the version: the requires of the three modules in
// each go.mod file of the repository, and the install commands with a version in docFiles.
func findPlaces(root string) ([]place, error) {
	var places []place
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		text, err := read(root, filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		for i, line := range strings.Split(text, "\n") {
			if m := requireLine.FindStringSubmatch(line); m != nil {
				places = append(places, place{filepath.ToSlash(rel), i + 1, m[2], m[4]})
			}
		}
		return nil
	})
	if err != nil {
		var re *releaseError
		if errors.As(err, &re) {
			return nil, err
		}
		return nil, fail("cannot search the go.mod files: %v", err)
	}
	for _, file := range docFiles {
		text, err := read(root, file)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for i, line := range strings.Split(text, "\n") {
			for _, m := range docVersion.FindAllStringSubmatch(line, -1) {
				places = append(places, place{file, i + 1, m[1], m[2]})
			}
		}
	}
	sort.SliceStable(places, func(i, j int) bool {
		if places[i].file != places[j].file {
			return places[i].file < places[j].file
		}
		return places[i].line < places[j].line
	})
	return places, nil
}

// currentVersion returns the one version that all places hold. Each version file must require
// the root module, and every place must hold the same version X.Y.Z.
func currentVersion(places []place) (version, error) {
	for _, file := range versionFiles {
		found := false
		for _, p := range places {
			if p.file == file && p.module == rootModule {
				found = true
			}
		}
		if !found {
			return version{}, fail("%s has no `require %s vX.Y.Z` line", file, rootModule)
		}
	}
	var current version
	var first place
	for i, p := range places {
		v, ok := parseVersion(strings.TrimPrefix(p.version, "v"))
		if !ok || !strings.HasPrefix(p.version, "v") {
			return version{}, fail("%s: %s is not a version vX.Y.Z", p, p.version)
		}
		if i == 0 {
			current, first = v, p
			continue
		}
		if v != current {
			lines := make([]string, len(places))
			for j, q := range places {
				lines[j] = "  " + q.String()
			}
			return version{}, fail("the places that hold the version do not agree (%s has %s, %s has %s); set them all to one version:\n%s",
				first.file, first.version, p.file, p.version, strings.Join(lines, "\n"))
		}
	}
	return current, nil
}

// setVersion returns text with the version of every place of the three modules set to v.
func setVersion(file, text string, v version) string {
	tag := "v" + v.String()
	if strings.HasSuffix(file, "go.mod") {
		lines := strings.Split(text, "\n")
		for i, line := range lines {
			if m := requireLine.FindStringSubmatch(line); m != nil {
				lines[i] = m[1] + m[2] + m[3] + tag + m[5]
			}
		}
		return strings.Join(lines, "\n")
	}
	return docVersion.ReplaceAllString(text, "${1}@"+tag)
}

// dateChangelog returns text with its [Unreleased] entries moved under `## [<version>] - <date>`,
// an empty [Unreleased] section above it, and the links of both sections.
func dateChangelog(text string, v version, date string, previous version, hasPrevious bool) (string, error) {
	if !datePattern.MatchString(date) {
		return "", fail("%q is not a date YYYY-MM-DD", date)
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	start := -1
	for i, line := range lines {
		if line == unreleased {
			start = i
			break
		}
	}
	if start < 0 {
		return "", fail("%s has no %s section", changelog, unreleased)
	}
	hasEntry := false
	for _, line := range lines[start+1 : sectionEnd(lines, start)] {
		if strings.HasPrefix(line, "- ") {
			hasEntry = true
		}
	}
	if !hasEntry {
		return "", fail("the %s section of %s has no entry; add the changes of the release first", unreleased, changelog)
	}
	link := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "[Unreleased]: ") {
			link = i
			break
		}
	}
	if link < 0 {
		return "", fail("%s has no [Unreleased] link", changelog)
	}
	versionLink := fmt.Sprintf("[%s]: %s/releases/tag/v%s", v, repository, v)
	if hasPrevious {
		versionLink = fmt.Sprintf("[%s]: %s/compare/v%s...v%s", v, repository, previous, v)
	}
	out := make([]string, 0, len(lines)+4)
	for i, line := range lines {
		switch i {
		case start:
			out = append(out, unreleased, "", fmt.Sprintf("## [%s] - %s", v, date))
		case link:
			out = append(out, fmt.Sprintf("[Unreleased]: %s/compare/v%s...HEAD", repository, v), versionLink)
		default:
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n") + "\n", nil
}

// datedSection returns the date and the body of the `## [<version>] - <date>` section, without
// its blank edges.
func datedSection(text string, v version) (date, body string, ok bool) {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	heading := fmt.Sprintf("## [%s] - ", v)
	for start, line := range lines {
		if !strings.HasPrefix(line, heading) {
			continue
		}
		date = strings.TrimSpace(strings.TrimPrefix(line, heading))
		if !datePattern.MatchString(date) {
			return "", "", false
		}
		body = strings.Join(lines[start+1:sectionEnd(lines, start)], "\n")
		return date, strings.Trim(body, "\n"), true
	}
	return "", "", false
}

// sectionEnd returns the index of the line after the section that starts at start: the next
// `## ` heading or the first link line.
func sectionEnd(lines []string, start int) int {
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") || linkLine.MatchString(lines[i]) {
			return i
		}
	}
	return len(lines)
}

func prepare(root, input, date string) (string, error) {
	places, err := findPlaces(root)
	if err != nil {
		return "", err
	}
	current, err := currentVersion(places)
	if err != nil {
		return "", err
	}
	tagList, err := tags(root)
	if err != nil {
		return "", err
	}
	newest, hasTag := newestTag(tagList)
	next, err := nextVersion(input, current, newest, hasTag)
	if err != nil {
		return "", err
	}
	text, err := read(root, changelog)
	if err != nil {
		return "", err
	}
	dated, err := dateChangelog(text, next, date, newest, hasTag)
	if err != nil {
		return "", err
	}
	if next != current {
		files := map[string]bool{}
		for _, p := range places {
			files[p.file] = true
		}
		for _, file := range sortedKeys(files) {
			text, err := read(root, file)
			if err != nil {
				return "", err
			}
			if err := write(root, file, setVersion(file, text, next)); err != nil {
				return "", err
			}
		}
	}
	if err := write(root, changelog, dated); err != nil {
		return "", err
	}
	return next.String(), nil
}

func status(root string) (string, error) {
	places, err := findPlaces(root)
	if err != nil {
		return "", err
	}
	current, err := currentVersion(places)
	if err != nil {
		return "", err
	}
	tagList, err := tags(root)
	if err != nil {
		return "", err
	}
	have := map[string]bool{}
	for _, tag := range tagList {
		have[tag] = true
	}
	tagged := true
	for _, prefix := range tagPrefixes {
		if !have[prefix+current.String()] {
			tagged = false
		}
	}
	text, err := read(root, changelog)
	if err != nil {
		return "", err
	}
	_, _, dated := datedSection(text, current)
	action := "skip"
	if dated && !tagged {
		action = "publish"
	}
	return current.String() + " " + action, nil
}

func notes(root, input string) (string, error) {
	v, ok := parseVersion(input)
	if !ok {
		return "", fail("%q is not a version X.Y.Z", input)
	}
	text, err := read(root, changelog)
	if err != nil {
		return "", err
	}
	_, body, ok := datedSection(text, v)
	if !ok {
		return "", fail("%s has no dated section for %s", changelog, v)
	}
	return body, nil
}

func tags(root string) ([]string, error) {
	cmd := exec.Command("git", "tag", "--list")
	cmd.Dir = root
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fail("git tag --list failed: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	var list []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			list = append(list, line)
		}
	}
	return list, nil
}

func read(root, file string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("cannot read %s: %w", file, err)
		}
		return "", fail("cannot read %s: %v", file, err)
	}
	return string(data), nil
}

func write(root, file, text string) error {
	path := filepath.Join(root, filepath.FromSlash(file))
	info, err := os.Stat(path)
	mode := fs.FileMode(0o644)
	if err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		return fail("cannot write %s: %v", file, err)
	}
	return nil
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// checkRoot makes sure that root is the root of this repository.
func checkRoot(root string) error {
	text, err := read(root, "go.mod")
	if err != nil {
		return fail("run the release steps from the root of the repository: %v", err)
	}
	m := moduleLine.FindStringSubmatch(text)
	if m == nil || m[1] != rootModule {
		return fail("run the release steps from the root of the repository: go.mod is not the module %s", rootModule)
	}
	return nil
}

// run returns the exit code and the text for stdout or stderr.
func run(args []string, root string) (int, string) {
	var output string
	var err error
	switch {
	case len(args) == 3 && args[0] == "prepare":
		if err = checkRoot(root); err == nil {
			output, err = prepare(root, args[1], args[2])
		}
	case len(args) == 1 && args[0] == "status":
		if err = checkRoot(root); err == nil {
			output, err = status(root)
		}
	case len(args) == 2 && args[0] == "notes":
		if err = checkRoot(root); err == nil {
			output, err = notes(root, args[1])
		}
	default:
		return 2, usage
	}
	if err != nil {
		return 1, "release: " + err.Error()
	}
	return 0, output
}

func main() {
	code, text := run(os.Args[1:], ".")
	if code == 0 {
		fmt.Println(text)
	} else {
		fmt.Fprintln(os.Stderr, text)
	}
	os.Exit(code)
}
