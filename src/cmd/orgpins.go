package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// An org dependency has no version of its own. The go command resolves it to
// the head of a branch: the branch this repository is on when the dependency has
// one of that name, and the dependency's default branch otherwise. A version
// file therefore records a placeholder, `vN.0.0` for the path's major, and a
// submodule or an action step names a branch.
//
// A frozen version defeats that. It names one commit of another repository and
// nothing moves it, so a consumer builds month-old code and reads the result as
// current. The rule holds for every repository in the org, and every repository
// in the org runs this pipeline, so this is where it is checked.

// orgPin is one place a file freezes an org dependency.
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

// checkOrgPins fails the run when a file in this repository freezes an org
// dependency at a version, a tag or a commit.
func checkOrgPins(root string) error {
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
		"dependency has one of that name, and its default branch otherwise. Record the\n"+
		"placeholder (v0.0.0, or vN.0.0 for a /vN path) in the version files, give the\n"+
		"submodule a branch, and name a branch on the action step", b.String())
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

// pinsInFile reports the pins in one file's text. A submodule section is read
// as a whole, since the url and the branch sit on different lines.
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
// that names no org path. OrgModulePrefixes is the one list of them.
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

// pinnedVersion reports the first version token on the line that is not the
// placeholder. A token is a version when it opens with v and a digit.
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

// isOrgPlaceholder reports whether version is the token a version file records
// for an org module: vN.0.0 for the path's major, and the `/go.mod` spelling
// go.sum writes beside it.
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
