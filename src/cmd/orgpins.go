package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// An org dependency has no version of its own.

// orgPin is a single place a file freezes an org dependency.
type orgPin struct {
	File string
	Line int
	What string
}

func (p orgPin) String() string {
	return fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.What)
}

// orgPinFiles names the files that can carry a pin, relative to the module root.
// A glob that matches nothing contributes nothing.
var orgPinFiles = []string{
	"go.mod",
	"go.sum",
	"vendor/modules.txt",
	".gitmodules",
	"action.yml",
	"action.yaml",
	".github/workflows/*.yml",
	".github/workflows/*.yaml",
	".github/actions/*/action.yml",
	".github/actions/*/action.yaml",
}

// moduleRoot is the directory the main module's go.mod sits in, which is where
// the files a pin can live in sit too.
func moduleRoot() string {
	return filepath.Dir(findGoMod())
}

// checkOrgPins rewrites every org pin under root so that it follows a branch.
// It fails the run only on a pin it cannot rewrite.
func checkOrgPins(root string) error {
	fixed, err := unpinOrgDeps(root)
	if err != nil {
		return err
	}
	if len(fixed) > 0 {
		logger.Output("⇒ org pins: unpinned %d", len(fixed))
		for _, p := range fixed {
			logger.Output("   %s", p)
		}
	}
	pins, err := findOrgPins(root)
	if err != nil || len(pins) == 0 {
		return err
	}
	var b strings.Builder
	for _, p := range pins {
		fmt.Fprintf(&b, "\n  %s", p)
	}
	return fmt.Errorf("org dependencies are pinned:%s\n\n"+
		"An org dependency follows a branch: this repository's own branch where the\n"+
		"dependency has one of that name, and its default branch otherwise. Give the\n"+
		"submodule a branch; go-toolchain cannot choose one for it", b.String())
}

// unpinOrgDeps rewrites each pin it can in the files under root, and reports
// what it rewrote. A go.mod or vendor version becomes the placeholder. An
// action step moves to @master. A submodule is left alone.
func unpinOrgDeps(root string) ([]orgPin, error) {
	var fixed []orgPin
	for _, pattern := range orgPinFiles {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			return nil, err
		}
		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				rel = path
			}
			text, pins := unpinFile(filepath.ToSlash(rel), string(data))
			if len(pins) == 0 {
				continue
			}
			if err := os.WriteFile(path, []byte(text), info.Mode().Perm()); err != nil {
				return nil, fmt.Errorf("unpin %s: %w", rel, err)
			}
			fixed = append(fixed, pins...)
		}
	}
	return fixed, nil
}

// unpinFile answers the text of a single file with its pins rewritten, and the
// pins it rewrote.
func unpinFile(name, text string) (string, []orgPin) {
	if strings.HasSuffix(name, ".gitmodules") {
		return text, nil
	}
	var pins []orgPin
	var out []string
	isSum := strings.HasSuffix(name, "go.sum")
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || orgPrefixIn(trimmed) == "" {
			out = append(out, line)
			continue
		}
		if ref, ok := pinnedActionRef(trimmed); ok {
			out = append(out, strings.Replace(line, "@"+ref, "@master", 1))
			pins = append(pins, orgPin{name, i + 1, "org action " + ref + " -> master"})
			continue
		}
		if isSum {
			if version, ok := pinnedVersion(trimmed); ok {
				pins = append(pins, orgPin{name, i + 1, "dropped the sum for " + version})
				continue
			}
			out = append(out, line)
			continue
		}
		rewritten, versions := unpinVersions(line)
		for _, v := range versions {
			pins = append(pins, orgPin{name, i + 1, "org module " + v})
		}
		out = append(out, rewritten)
	}
	return strings.Join(out, "\n"), pins
}

// unpinVersions replaces each version token that follows an org module path
// with the placeholder for that path. It keeps the line's own spacing.
func unpinVersions(line string) (string, []string) {
	var b strings.Builder
	var changes []string
	prev := ""
	rest := line
	for rest != "" {
		start := strings.IndexFunc(rest, func(r rune) bool { return r != ' ' && r != '\t' })
		if start < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:start])
		rest = rest[start:]
		end := strings.IndexAny(rest, " \t")
		if end < 0 {
			end = len(rest)
		}
		word := rest[:end]
		rest = rest[end:]
		if looksLikeVersionToken(word) && !isOrgPlaceholder(word) && isOrgModulePath(prev) {
			placeholder := orgPlaceholderFor(prev)
			changes = append(changes, word+" -> "+placeholder)
			word = placeholder
		}
		b.WriteString(word)
		prev = word
	}
	return b.String(), changes
}

func isOrgModulePath(word string) bool {
	for _, prefix := range OrgModulePrefixes {
		if strings.HasPrefix(word, prefix) {
			return true
		}
	}
	return false
}

func orgPlaceholderFor(path string) string {
	last := path[strings.LastIndex(path, "/")+1:]
	if len(last) > 1 && last[0] == 'v' {
		if n, err := strconv.Atoi(last[1:]); err == nil && n >= 2 {
			return fmt.Sprintf("v%d.0.0", n)
		}
	}
	return "v0.0.0"
}

// findOrgPins reports every pin under root, in file order.
func findOrgPins(root string) ([]orgPin, error) {
	var pins []orgPin
	for _, pattern := range orgPinFiles {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			return nil, err
		}
		for _, path := range matches {
			data, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				rel = path
			}
			pins = append(pins, pinsInFile(filepath.ToSlash(rel), string(data))...)
		}
	}
	return pins, nil
}

// pinsInFile reports the pins in a single file's text. A submodule section is
// read as a whole, since the url and the branch sit on different lines.
func pinsInFile(name, text string) []orgPin {
	var pins []orgPin
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(name, ".gitmodules") {
		return submodulePins(name, lines)
	}
	for i, line := range lines {
		text := strings.TrimSpace(line)
		if text == "" || orgPrefixIn(text) == "" {
			continue
		}
		if ref, ok := pinnedActionRef(text); ok {
			pins = append(pins, orgPin{name, i + 1, "org action pinned to " + ref})
			continue
		}
		if version, ok := pinnedVersion(text); ok {
			pins = append(pins, orgPin{name, i + 1, "org module pinned to " + version})
		}
	}
	return pins
}

// orgPrefixIn returns the org module prefix the line mentions, or "" for a line
// that names no org path. OrgModulePrefixes holds them.
func orgPrefixIn(text string) string {
	// The owner segment alone, because an action step names the owner without the
	// host and a git remote spells the host with a colon.
	for _, prefix := range OrgModulePrefixes {
		if strings.Contains(text, orgOwner(prefix)) {
			return prefix
		}
	}
	return ""
}

// orgOwner returns the owner segment of an org prefix, which is how an action
// step names the org.
func orgOwner(prefix string) string {
	_, owner, _ := strings.Cut(strings.TrimSuffix(prefix, "/"), "/")
	return owner + "/"
}

// submodulePins reports each org submodule that names no branch to follow.
func submodulePins(name string, lines []string) []orgPin {
	var pins []orgPin
	section, sectionLine, isOrg, tracked := "", 0, false, false
	flush := func() {
		if isOrg && !tracked {
			pins = append(pins, orgPin{name, sectionLine, "submodule " + section + " names no branch to follow"})
		}
	}
	for i, line := range lines {
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, "[submodule ") {
			flush()
			section, sectionLine, isOrg, tracked = submoduleName(text), i+1, false, false
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "url":
			isOrg = orgPrefixIn(value) != ""
		case "branch":
			tracked = strings.TrimSpace(value) != ""
		}
	}
	flush()
	return pins
}

// submoduleName reads the name out of a `[submodule "name"]` header.
func submoduleName(header string) string {
	if _, rest, ok := strings.Cut(header, `"`); ok {
		if name, _, ok := strings.Cut(rest, `"`); ok {
			return name
		}
	}
	return header
}

// pinnedActionRef reports the ref of a `uses:` step that takes an org action at
// a tag or a commit. A branch, and the orphan tags the org publishes its actions
// on (`actions@typescript#latest`), are what such a step is supposed to name.
func pinnedActionRef(text string) (string, bool) {
	if !strings.Contains(text, "uses:") {
		return "", false
	}
	_, rest, ok := strings.Cut(text, orgOwner(orgPrefixIn(text)))
	if !ok {
		return "", false
	}
	_, ref, ok := strings.Cut(rest, "@")
	if !ok {
		return "", false
	}
	fields := strings.Fields(ref)
	if len(fields) == 0 {
		return "", false
	}
	ref = strings.Trim(fields[0], `"'`)
	// An org action publishes to a per-directory orphan tag, `actions@name#latest`.
	if strings.Contains(ref, "#") {
		return "", false
	}
	if isSemverTag(ref) || isCommitSHA(ref) {
		return ref, true
	}
	return "", false
}

// pinnedVersion reports the earliest version token on the line that is not
// the placeholder. A token is a version when it opens with v and a digit.
func pinnedVersion(text string) (string, bool) {
	for _, word := range strings.Fields(text) {
		word = strings.TrimSuffix(word, ",")
		if !looksLikeVersionToken(word) || isOrgPlaceholder(word) {
			continue
		}
		return word, true
	}
	return "", false
}

// looksLikeVersionToken reports whether word is where a version is written.
func looksLikeVersionToken(word string) bool {
	return len(word) > 1 && word[0] == 'v' && word[1] >= '0' && word[1] <= '9'
}

func isOrgPlaceholder(version string) bool {
	version = strings.TrimSuffix(version, "/go.mod")
	major, rest, ok := strings.Cut(strings.TrimPrefix(version, "v"), ".")
	if !ok || rest != "0.0" {
		return false
	}
	if _, err := strconv.Atoi(major); err != nil {
		return false
	}
	return true
}

// isSemverTag reports whether ref is a release tag.
func isSemverTag(ref string) bool {
	return looksLikeVersionToken(ref)
}

// isCommitSHA reports whether ref is a full commit hash.
func isCommitSHA(ref string) bool {
	if len(ref) != 40 {
		return false
	}
	for _, c := range ref {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
