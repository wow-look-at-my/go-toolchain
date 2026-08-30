package cmd

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The real failure, copied from a CI host-build job: an
// "invalid package name" per unreadable package, then the cascade of redeclared
// and undefined symbols that makes it read like a source error.
const realInvalidPackageNameErr = `package load errors:
-: # github.com/wow-look-at-my/go-toolchain/src/trace
src/trace/export.go:11:2: trace redeclared in this block
src/trace/export.go:10:2: 	other declaration of trace
src/trace/provider.go:13:2: could not import go.opentelemetry.io/otel/sdk/resource (invalid package name: "")
src/trace/provider.go:15:10: could not import go.opentelemetry.io/otel/semconv/v1.24.0 (invalid package name: "")
src/trace/provider_otlp.go:8:2: could not import go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp (invalid package name: "")
src/trace/provider.go:142:17: undefined: sdktrace.SpanExporter
src/trace/propagation.go:15:42: undefined: trace.TraceID`

// The same failure reported the other way, copied from a CI host-build job.
// Matching only the signature above left this report surfacing as a source
// error, which is what it looks like and is not.
const realInternalImportErr = `package load errors:
src/cache/web_resilience.go:6:2: could not import math/rand/v2 (reading /home/runner/.cache/go-toolchain/buildcache/mnt/6bb105400a2cd4db3212a4d69ade5697293033bd58085dbf9b286d6a78262d31: internal error in importing "math/rand/v2" (function with type parameters cannot have a receiver); please report an issue)
src/cache/web_resilience.go:187:20: undefined: rand`

func TestIsUnreadableExportData(t *testing.T) {
	assert.True(t, isUnreadableExportData(errors.New(realInvalidPackageNameErr)))
	assert.True(t, isUnreadableExportData(errors.New(realInternalImportErr)))
	assert.False(t, isUnreadableExportData(nil))

	// A genuine source error must never be routed to the retry, or it gets reported as infrastructure.
	assert.False(t, isUnreadableExportData(errors.New(`package load errors:
src/cmd/foo.go:12:3: undefined: Bar`)))
	assert.False(t, isUnreadableExportData(errors.New("corrupt index")),
		"the module-index failure is a different signature with a different cure")
}

// The reader has to be able to tell the reports apart: they come from different
// decode stages, and a run that keeps hitting the same marker is a different
// story from a run that alternates.
func TestExportDataSignature(t *testing.T) {
	assert.Equal(t, invalidPackageNameMarker, exportDataSignature(errors.New(realInvalidPackageNameErr)))
	assert.Equal(t, internalImportErrorMarker, exportDataSignature(errors.New(realInternalImportErr)))
	assert.Empty(t, exportDataSignature(nil))
	assert.Empty(t, exportDataSignature(errors.New("undefined: Bar")))
}

// The warning names what could not be read, so a reader can tell an isolated case
// from a tier that is systematically serving bad entries.
func TestUnreadableExportPackages(t *testing.T) {
	assert.Equal(t, []string{
		"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp",
		"go.opentelemetry.io/otel/sdk/resource",
		"go.opentelemetry.io/otel/semconv/v1.24.0",
	}, unreadableExportPackages(errors.New(realInvalidPackageNameErr)))

	// The internal-error report puts the cache file path between the package and the signature, so the parse reaches past it.
	assert.Equal(t, []string{"math/rand/v2"}, unreadableExportPackages(errors.New(realInternalImportErr)))

	assert.Empty(t, unreadableExportPackages(nil))
	assert.Empty(t, unreadableExportPackages(errors.New("some other failure")))
}

func TestDisableSharedBuildCache(t *testing.T) {
	t.Run("reports false when the shared tier is not in play", func(t *testing.T) {
		t.Setenv("GOCACHEPROG", "")
		assert.False(t, disableSharedBuildCache(), "nothing to disable means no retry is warranted")
	})

	t.Run("unsets it and reports true", func(t *testing.T) {
		t.Setenv("GOCACHEPROG", "/usr/local/bin/go-toolchain cacheprog")
		require.True(t, disableSharedBuildCache())
		assert.Empty(t, os.Getenv("GOCACHEPROG"))
		// Idempotent: a repeat call finds nothing, so the retry cannot happen again.
		assert.False(t, disableSharedBuildCache())
	})
}

// The message the source retry could not clear must name what was unreadable and
// rule out the easy answers -- never leave the caller with the raw cascade.
func TestUnreadableExportDataError(t *testing.T) {
	base := errors.New(realInvalidPackageNameErr)
	msg := unreadableExportDataError(base).Error()
	assert.Contains(t, msg, "export data")
	assert.Contains(t, msg, "NOT your source")
	assert.Contains(t, msg, "go clean -cache")
	assert.Contains(t, msg, "go.opentelemetry.io/otel/sdk/resource")
	assert.ErrorIs(t, unreadableExportDataError(base), base, "the original load errors must stay reachable")

	// With no package names parsed the message still stands on its own.
	generic := unreadableExportDataError(fmt.Errorf(`x (%s)`, invalidPackageNameMarker))
	assert.Contains(t, generic.Error(), "export data")

	// The message names the signature it matched, and the package, for either report.
	internal := unreadableExportDataError(errors.New(realInternalImportErr)).Error()
	assert.Contains(t, internal, internalImportErrorMarker)
	assert.Contains(t, internal, "math/rand/v2")
}
