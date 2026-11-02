// ReleaseEnv names the buildhost release number for a build that already has one.

package cmd

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// ReleaseEnv names the buildhost release number for a build that already has one.
const ReleaseEnv = "GO_TOOLCHAIN_RELEASE_VERSION"

// The fork's go command stamps these as the build settings release and release.revision.
const (
	gosmoReleaseEnv         = "GOSMOPOLITAN_RELEASE"
	gosmoReleaseRevisionEnv = "GOSMOPOLITAN_RELEASE_REVISION"
)

// releaseRunLock prefixes the run lock.
const releaseRunLock = "buildhost-release"

func releaseRunLockName(project string) string {
	return releaseRunLock + ":" + project
}

var (
	releaseOnce  sync.Once
	releaseValue string
	releaseErr   error
)

// resolveRelease answers the buildhost release number this run builds, once per process.
// Outside CI with no ReleaseEnv there is no release, and it answers "".
// binary is the first binary the build links. Its publish project holds the release.
func resolveRelease(binary string) (string, error) {
	releaseOnce.Do(func() {
		releaseValue, releaseErr = lookupRelease(binary)
		if releaseErr == nil && releaseValue != "" {
			releaseErr = writeReleaseOutput(releaseValue)
		}
	})
	return releaseValue, releaseErr
}

func lookupRelease(binary string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(ReleaseEnv)); v != "" {
		return usableRelease(v)
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return "", nil
	}
	return createRunRelease(binary)
}

// publishProject is the buildhost project the publish uploads binary to. A
// release made in another project counts its numbers apart, and the publish
// then finds the number already published in its own project.
func publishProject(repo, binary string) string {
	repo = strings.ToLower(repo)
	binary = strings.ToLower(binary)
	switch {
	case binary == "" || binary == repo:
		return repo
	case strings.HasPrefix(binary, repo+"-"):
		return repo + "/" + strings.TrimPrefix(binary, repo+"-")
	default:
		return repo + "/" + binary
	}
}

// usableRelease rejects what a build setting cannot carry, the same rule as a revision.
func usableRelease(v string) (string, error) {
	if usableRevision(v) != v {
		return "", fmt.Errorf("release number %q holds whitespace or a quote, which a build setting cannot carry", v)
	}
	return v, nil
}

var (
	releaseEnvOnce sync.Once
	releaseEnvErr  error
)

// ensureReleaseEnvFunc is ensureReleaseEnv, as a seam: a mock build in a test must never create a buildhost release.
var ensureReleaseEnvFunc = ensureReleaseEnv

// ensureReleaseEnv puts the release number and its commit in this process's
// environment, which every go command it starts inherits. The fork's go
// command then stamps both into the build info of every binary it links.
// In CI a missing number or commit fails the build. Outside CI with no
// ReleaseEnv there is no release, and it warns once.
func ensureReleaseEnv(binary string) error {
	releaseEnvOnce.Do(func() {
		rel, err := resolveRelease(binary)
		if err != nil {
			releaseEnvErr = err
			return
		}
		if rel == "" {
			logger.Warn("⇒ Warning: a build outside CI has no buildhost release, so its binaries carry no release number; set %s to give them one", ReleaseEnv)
			return
		}
		rev := resolveRevision()
		if rev == "" {
			releaseEnvErr = fmt.Errorf("release %s: this build knows no commit to stamp beside it; set %s or GITHUB_SHA", rel, RevisionEnv)
			return
		}
		if err := os.Setenv(gosmoReleaseEnv, rel); err != nil {
			releaseEnvErr = err
			return
		}
		releaseEnvErr = os.Setenv(gosmoReleaseRevisionEnv, rev)
	})
	return releaseEnvErr
}

// createRunRelease creates the buildhost release this run publishes, or answers
// the one another job of the same run attempt already created.
func createRunRelease(binary string) (string, error) {
	store := strings.TrimSuffix(os.Getenv(runLockStoreEnv), "/")
	if store == "" {
		store = defaultRunLockStore
	}
	fail := func(err error) (string, error) { return "", fmt.Errorf("buildhost release: %s: %w", store, err) }
	token, err := runLockToken(store)
	if err != nil {
		return fail(err)
	}
	repository := os.Getenv("GITHUB_REPOSITORY")
	repo := repository[strings.LastIndex(repository, "/")+1:]
	if repo == "" {
		return fail(fmt.Errorf("GITHUB_REPOSITORY is unset, so the build cannot name its project"))
	}
	project := publishProject(repo, binary)
	// One lock per project: jobs that publish to other projects count their numbers apart.
	lock := runLockBody{
		Repository: repository,
		RunID:      os.Getenv("GITHUB_RUN_ID"),
		RunAttempt: os.Getenv("GITHUB_RUN_ATTEMPT"),
		Name:       releaseRunLockName(project),
	}
	query := url.Values{"repository": {lock.Repository}, "run_id": {lock.RunID}, "run_attempt": {lock.RunAttempt}, "name": {lock.Name}}
	var held runLockBody
	if err := runLockDo(http.MethodGet, store+"/api/v1/run-locks?"+query.Encode(), token, nil, &held); err != nil {
		return fail(err)
	}
	if held.Found && held.Value != "" {
		return usableRelease(held.Value)
	}
	body, err := json.Marshal(map[string]string{
		"git_branch": os.Getenv("GITHUB_REF_NAME"),
		"git_commit": os.Getenv("GITHUB_SHA"),
	})
	if err != nil {
		return fail(err)
	}
	var created struct {
		Version string `json:"version"`
	}
	if err := releaseDo(store+"/api/v1/projects/"+url.PathEscape(project)+"/releases", token, body, &created); err != nil {
		return fail(fmt.Errorf("creating the release of %s: %w", project, err))
	}
	if created.Version == "" {
		return fail(fmt.Errorf("creating the release of %s: the answer names no version", project))
	}
	lock.Value = created.Version
	claim, err := json.Marshal(lock)
	if err != nil {
		return fail(err)
	}
	var won runLockBody
	if err := runLockDo(http.MethodPost, store+"/api/v1/run-locks", token, claim, &won); err != nil {
		return fail(err)
	}
	if won.Value == "" {
		return fail(fmt.Errorf("the run lock holds an empty release number"))
	}
	return usableRelease(won.Value)
}

// releaseDo posts a create-release request through buildhostDo.
func releaseDo(target, token string, body []byte, answer any) error {
	reply, err := buildhostDo(http.MethodPost, target, token, body)
	if err != nil {
		return err
	}
	if reply.Code != http.StatusCreated {
		return fmt.Errorf("%s: %s", reply.Status, strings.TrimSpace(string(reply.Body)))
	}
	return json.Unmarshal(reply.Body, answer)
}

// writeReleaseOutput hands the number to the action's publish, which releases under the same number.
// A number the publish cannot read would ship the binaries under another release, so a failed write fails the build.
func writeReleaseOutput(v string) error {
	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("release %s: recording it in GITHUB_OUTPUT for the publish: %w", v, err)
	}
	if _, err := fmt.Fprintf(f, "release_version=%s\n", v); err != nil {
		f.Close()
		return fmt.Errorf("release %s: recording it in GITHUB_OUTPUT for the publish: %w", v, err)
	}
	return f.Close()
}

// verifyReleaseStamp fails a build whose binary lacks the release this process
// handed the go command. A go command without gosmopolitan's release settings
// ignores the variables, and its binary would otherwise ship unstamped.
func verifyReleaseStamp(r runner.CommandRunner, job buildJob, path string) error {
	rel := os.Getenv(gosmoReleaseEnv)
	if rel == "" || isPackageArchive(path) {
		return nil
	}
	rev := os.Getenv(gosmoReleaseRevisionEnv)
	var got map[string]string
	if hasMagic(path, wasmMagic) {
		// debug/buildinfo reads no WebAssembly module, so `go version -m` cannot answer for one.
		settings, err := rawBuildInfoSettings(path)
		if err != nil {
			return fmt.Errorf("reading the build info of %s: %w", path, err)
		}
		got = settings
	} else {
		args := append(append([]string{}, job.goCmd[1:]...), "version", "-m", path)
		proc, err := runner.Cmd(job.goCmd[0], args...).
			WithEnv("GOTOOLCHAIN", "local").
			WithEnv("GOROOT", job.goroot).
			WithQuiet().
			Run(r)
		if err != nil {
			return fmt.Errorf("reading the build info of %s: %w", path, err)
		}
		stdout, _ := io.ReadAll(proc.Stdout())
		stderr, _ := io.ReadAll(proc.Stderr())
		if err := proc.Wait(); err != nil {
			return fmt.Errorf("reading the build info of %s: %w\n%s", path, err, bytes.TrimSpace(stderr))
		}
		got = buildInfoSettings(string(stdout))
	}
	if got["release"] != rel || got["release.revision"] != rev {
		return fmt.Errorf("%s carries release %q and revision %q, not %s and %s: the go command %s did not stamp the release, which needs gosmopolitan's release build settings",
			path, got["release"], got["release.revision"], rel, rev, job.goCmd[0])
	}
	return nil
}

// isPackageArchive reports whether path is the archive a library-only
// module's compile check writes.
func isPackageArchive(path string) bool {
	return hasMagic(path, packageArchiveMagic)
}

// wasmMagic opens every WebAssembly module.
const wasmMagic = "\x00asm"

// hasMagic reports whether the file at path starts with magic.
func hasMagic(path, magic string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, len(magic))
	if _, err := io.ReadFull(f, head); err != nil {
		return false
	}
	return string(head) == magic
}

// The blob a go1.18 or later linker writes: buildInfoMagic, a pointer size.
const (
	buildInfoMagic       = "\xff Go buildinf:"
	buildInfoHeaderSize  = 32
	buildInfoFlagsOffset = 15
	buildInfoFlagInline  = 0x2
	buildInfoFrame       = 16 // the sentinel on each side of the modinfo
)

// rawBuildInfoSettings reads the build settings off the bytes of a binary
// whose format debug/buildinfo does not read.
func rawBuildInfoSettings(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	start := bytes.Index(data, []byte(buildInfoMagic))
	if start < 0 || len(data) < start+buildInfoHeaderSize {
		// The wasm linker writes no build-info header. The modinfo string is still in the data, between its sentinels.
		if mod, ok := framedModinfo(data); ok {
			return buildInfoSettings(mod), nil
		}
		return nil, fmt.Errorf("no Go build info in %s", path)
	}
	if data[start+buildInfoFlagsOffset]&buildInfoFlagInline == 0 {
		return nil, fmt.Errorf("the build info of %s holds pointers, not inline strings", path)
	}
	_, rest, err := varintString(data[start+buildInfoHeaderSize:])
	if err != nil {
		return nil, fmt.Errorf("the build info of %s: version: %w", path, err)
	}
	mod, _, err := varintString(rest)
	if err != nil {
		return nil, fmt.Errorf("the build info of %s: modinfo: %w", path, err)
	}
	if len(mod) >= 2*buildInfoFrame+1 && mod[len(mod)-buildInfoFrame-1] == '\n' {
		mod = mod[buildInfoFrame : len(mod)-buildInfoFrame]
	}
	return buildInfoSettings(mod), nil
}

// The sentinels the go command puts on each side of runtime.modinfo.
const (
	modinfoStart = "\x30\x77\xaf\x0c\x92\x74\x08\x02\x41\xe1\xc1\x07\xe6\xd6\x18\xe6"
	modinfoEnd   = "\xf9\x32\x43\x31\x86\x18\x20\x72\x00\x82\x42\x10\x41\x16\xd8\xf2"
)

// framedModinfo finds the modinfo string between its sentinels.
func framedModinfo(data []byte) (string, bool) {
	start := bytes.Index(data, []byte(modinfoStart))
	if start < 0 {
		return "", false
	}
	body := data[start+len(modinfoStart):]
	end := bytes.Index(body, []byte(modinfoEnd))
	if end < 0 {
		return "", false
	}
	return string(body[:end]), true
}

// varintString reads a varint length and that many bytes, and answers the string and what follows it.
func varintString(data []byte) (string, []byte, error) {
	length, n := binary.Uvarint(data)
	if n <= 0 || length > uint64(len(data)-n) {
		return "", nil, fmt.Errorf("a string length the bytes do not hold")
	}
	return string(data[n : n+int(length)]), data[n+int(length):], nil
}

const packageArchiveMagic = "!<arch>\n"

// buildInfoSettings reads the build settings from `go version -m` output, whose setting lines are "\tbuild\tkey=value".
func buildInfoSettings(out string) map[string]string {
	settings := map[string]string{}
	for line := range strings.SplitSeq(out, "\n") {
		kind, setting, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || kind != "build" {
			continue
		}
		if key, value, ok := strings.Cut(setting, "="); ok {
			settings[key] = value
		}
	}
	return settings
}
