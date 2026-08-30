module github.com/wow-look-at-my/go-toolchain

go 1.27

require (
	github.com/Masterminds/semver/v3 v3.5.0
	github.com/go-git/go-git/v5 v5.19.2
	github.com/hanwen/go-fuse/v2 v2.11.0
	github.com/mattn/go-isatty v0.0.24
	github.com/pierrec/lz4/v4 v4.1.29
	github.com/pmezard/go-difflib v1.0.0
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.10
	github.com/stretchr/testify v1.12.1
	github.com/wow-look-at-my/ansi-writer v0.0.0-20260811021111-443bcdc98174 // go-toolchain:auto-branch
	github.com/wow-look-at-my/dats v0.0.0-20260830194301-e4eb47ca8beb // go-toolchain:auto-branch
	github.com/wow-look-at-my/go-containers v0.0.0-20260826161058-40a3d1ef3d41 // go-toolchain:auto-branch
	github.com/wow-look-at-my/go-mmap v0.0.0-20260824191519-1d122070e11e // go-toolchain:auto-branch
	github.com/wow-look-at-my/is-this-an-agent v0.0.0-20260823195827-77471848149d // go-toolchain:auto-branch
	go.opentelemetry.io/otel v1.45.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.45.0
	go.opentelemetry.io/otel/sdk v1.45.0
	go.opentelemetry.io/otel/trace v1.45.0
	golang.org/x/mod v0.40.0
	golang.org/x/sys v0.47.0
	golang.org/x/tools v0.49.0
	gopkg.in/yaml.v3 v3.0.1
	gotest.tools/gotestsum v1.13.0
)

require (
	dario.cat/mergo v1.0.0 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/ProtonMail/go-crypto v1.1.6 // indirect
	github.com/bitfield/gotestdox v0.2.2 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/cyphar/filepath-securejoin v0.6.1 // indirect
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376 // indirect
	github.com/go-git/go-billy/v5 v5.9.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/kevinburke/ssh_config v1.2.0 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/pjbgf/sha1cd v0.6.0 // indirect
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3 // indirect
	github.com/skeema/knownhosts v1.3.1 // indirect
	github.com/wow-look-at-my/yaml-fixed v0.0.0-20260806231905-d99b869b77a1 // indirect
	github.com/xanzy/ssh-agent v0.3.3 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.45.0 // indirect
	go.opentelemetry.io/otel/metric v1.45.0 // indirect
	go.opentelemetry.io/proto/otlp v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/term v0.45.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260803160001-6ac0973c030d // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260803160001-6ac0973c030d // indirect
	google.golang.org/grpc v1.83.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
)

replace gonum.org/v1/gonum v0.17.0 => github.com/gonum/gonum v0.17.0

replace modernc.org/cc/v4 v4.27.3 => gitlab.com/cznic/cc/v4 v4.27.3

replace modernc.org/ccgo/v4 v4.32.4 => gitlab.com/cznic/ccgo/v4 v4.32.4

replace modernc.org/fileutil v1.4.0 => gitlab.com/cznic/fileutil v1.4.0

replace modernc.org/gc/v2 v2.6.5 => gitlab.com/cznic/gc/v2 v2.6.5

replace modernc.org/gc/v3 v3.1.2 => gitlab.com/cznic/gc/v3 v3.1.2

replace modernc.org/goabi0 v0.2.0 => gitlab.com/cznic/goabi0 v0.2.0

replace modernc.org/libc v1.72.0 => gitlab.com/cznic/libc v1.72.0

replace modernc.org/mathutil v1.7.1 => gitlab.com/cznic/mathutil v1.7.1

replace modernc.org/memory v1.11.0 => gitlab.com/cznic/memory v1.11.0

replace modernc.org/opt v0.1.4 => gitlab.com/cznic/opt v0.1.4

replace modernc.org/sortutil v1.2.1 => gitlab.com/cznic/sortutil v1.2.1

replace modernc.org/sqlite v1.50.0 => gitlab.com/cznic/sqlite v1.50.0

replace modernc.org/strutil v1.2.1 => gitlab.com/cznic/strutil v1.2.1

replace modernc.org/token v1.1.0 => gitlab.com/cznic/token v1.1.0

replace dario.cat/mergo v1.0.0 => github.com/imdario/mergo v1.0.0
