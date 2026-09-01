package cache

import (
	"github.com/wow-look-at-my/go-s3-server/cacheclient"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// The remote tier and the wire protocol live in the cacheclient module,
// alongside the server that answers it, so a header or an endpoint cannot
// change without the other. This package keeps the GOCACHEPROG protocol
// server and the local tiers, and reaches the remote through these names.
//
// The counters are shared rather than duplicated: the local tiers report
// through the same types the remote tier does, so a snapshot covers the run.
type (
	AtomicCounter        = cacheclient.AtomicCounter
	BatchEntry           = cacheclient.BatchEntry
	CacheStats           = cacheclient.CacheStats
	ConcurrencySnapshot  = cacheclient.ConcurrencySnapshot
	ConcurrencyTracker   = cacheclient.ConcurrencyTracker
	LatencySnapshot      = cacheclient.LatencySnapshot
	LatencyStats         = cacheclient.LatencyStats
	LatencyStatsSnapshot = cacheclient.LatencyStatsSnapshot
	LatencyTracker       = cacheclient.LatencyTracker
	WebBackend           = cacheclient.WebBackend
	WebConfig            = cacheclient.WebConfig
	WebSummary           = cacheclient.WebSummary
)

// MaxConnsPerHost is the remote tier's HTTP connection pool size.
const MaxConnsPerHost = cacheclient.MaxConnsPerHost

// buildIDHashSize is the width of the action hash a build id carries.
const buildIDHashSize = cacheclient.BuildIDHashSize

// errLogged marks an error already reported; callers must not log it again.
var errLogged = cacheclient.ErrLogged

// NewWebBackend builds the remote tier, or nil when no bucket is configured.
var NewWebBackend = cacheclient.NewWebBackend

// ConfigFromEnv parses GO_BUILDCACHE_CONFIG; an empty Bucket means no remote.
var ConfigFromEnv = cacheclient.ConfigFromEnv

// The protocol helpers this package still applies to bodies it handles
// locally: the same guards, so a local tier can never serve what the remote
// tier would refuse.
var (
	archiveExportInfo     = cacheclient.ArchiveExportInfo
	buildIDMatchesAction  = cacheclient.BuildIDMatchesAction
	decompressData        = cacheclient.Decompress
	describeData          = cacheclient.DescribeData
	expectedBuildIDAction = cacheclient.ExpectedBuildIDAction
	isGoModuleIndex       = cacheclient.IsGoModuleIndex
	outputIDMatches       = cacheclient.OutputIDMatches
	shortID               = cacheclient.ShortID
)

// Route the client's diagnostics into this build's logger. It writes
// nothing by itself, and cacheprog owns stdout as a protocol channel.
func init() {
	cacheclient.SetLogger(toolchainLogger{})
}

// toolchainLogger adapts src/logger to the client's Logger.
type toolchainLogger struct{}

func (toolchainLogger) Infof(format string, args ...any) { logger.Info(format, args...) }

func (toolchainLogger) Warnf(format string, args ...any) { logger.Warn(format, args...) }

func (toolchainLogger) Debugf(format string, args ...any) {
	logger.WithSubsystem("cache").Debug(format, args...)
}
