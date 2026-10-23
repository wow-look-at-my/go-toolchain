// ensureDirectFallback appends ",direct" so a module the proxy does not carry still resolves.

package main

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/cmd"
	"github.com/wow-look-at-my/go-toolchain/src/golangorg"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// ensureDirectFallback appends ",direct" so a module the proxy does not carry
// still resolves.
func ensureDirectFallback(goproxy string) string {
	if strings.HasSuffix(goproxy, "|direct") {
		return strings.TrimSuffix(goproxy, "|direct") + ",direct"
	}
	if !strings.Contains(goproxy, "direct") {
		return goproxy + ",direct"
	}
	return goproxy
}

// proxyEnvVars are the Go environment variables that configureGoEnv manages.
var proxyEnvVars = []string{"GOPROXY", "GOSUMDB", "GONOSUMDB", "GONOSUMCHECK"}

// runEnv is the fork's variable naming the build's run, as owner/repo/run-id/attempt.
const runEnv = "GOSMOPOLITAN_RUN"

// nameRun exports a run for this invocation, so every go command it starts
// resolves each org module's branch head once instead of once per command.
func nameRun() {
	// A run the caller or CI named is the one every go command must share.
	if os.Getenv(runEnv) != "" {
		return
	}
	// The fork reads the run from the GitHub variables when both are set, so a
	// CI run keeps the identity its jobs already share.
	if os.Getenv("GITHUB_RUN_ID") != "" && os.Getenv("GITHUB_RUN_ATTEMPT") != "" {
		return
	}
	os.Setenv(runEnv, localRunName())
}

// localRunName names this invocation in runEnv's shape.
func localRunName() string {
	return fmt.Sprintf("go-toolchain/local/%d-%d-%d/1", os.Getpid(), time.Now().UnixNano(), localRunSeq.Add(1))
}

// localRunSeq numbers each local run of this process.
var localRunSeq atomic.Uint64

// pinGoEnv sets what every run holds, whatever the caller set: no toolchain
// download, no telemetry upload, and no setting that names golang.org.
func pinGoEnv() error {
	golangorg.Install()
	os.Setenv("GOTOOLCHAIN", "local")
	os.Setenv("GOTELEMETRY", "off")
	return golangorg.CheckEnv()
}

// configureGoEnv sets GOPROXY, GOSUMDB, GONOSUMDB, and GONOSUMCHECK from the
// GOPROXY/GOSUMDB env vars. With neither set, it uses GOPROXY=direct with
// GOSUMDB=off. GO_PROXY_CONFIG is ignored: the org proxy it names is gone.
func configureGoEnv() error {
	if err := pinGoEnv(); err != nil {
		return err
	}
	nameRun()
	proxyLog := logger.WithSubsystem("proxy")
	defer func() {
		for _, k := range proxyEnvVars {
			if v := os.Getenv(k); v != "" {
				proxyLog.Debug("%s=%s", k, v)
			}
		}
	}()

	goproxy := os.Getenv("GOPROXY")
	gosumdb := os.Getenv("GOSUMDB")

	// GOPROXY: use configured value with "|direct" fallback, or default to "direct".
	if goproxy != "" && goproxy != "direct" && goproxy != "off" {
		os.Setenv("GOPROXY", ensureDirectFallback(goproxy))
	} else {
		os.Setenv("GOPROXY", "direct")
	}

	// GOSUMDB: a configured value names a database off golang.org, because
	// pinGoEnv refused every other.
	if gosumdb != "" && gosumdb != "off" {
		os.Setenv("GOSUMDB", gosumdb)
		// A sumdb holds public modules only, so it refuses an org module.
		os.Setenv("GONOSUMDB", orgSumDBExemptions())
		os.Setenv("GONOSUMCHECK", orgSumDBExemptions())
		return nil
	}

	os.Setenv("GOSUMDB", "off")
	os.Setenv("GONOSUMDB", "*")
	os.Setenv("GONOSUMCHECK", "*")
	return nil
}

// orgSumDBExemptions is the GONOSUMDB glob list covering every org module path.
// GONOSUMDB and not GOPRIVATE: GOPRIVATE would also take the module off the
// proxy and send the fetch straight to git.
func orgSumDBExemptions() string {
	globs := make([]string, 0, len(cmd.OrgModulePrefixes))
	for _, prefix := range cmd.OrgModulePrefixes {
		globs = append(globs, strings.TrimSuffix(prefix, "/")+"/*")
	}
	return strings.Join(globs, ",")
}
