package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
)

func TestEnforceSqliteFork_RedirectsABareRequire(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require modernc.org/sqlite v1.50.1
`)

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceSqliteFork(mock)
	require.NoError(t, err)
	assert.True(t, changed)

	f, err := modfile.Parse("go.mod", readGoMod(t), nil)
	require.NoError(t, err)
	require.Len(t, f.Replace, 1)
	assert.Equal(t, modernSqliteModule, f.Replace[0].Old.Path)
	assert.Equal(t, sqliteForkModule, f.Replace[0].New.Path)
	assert.Equal(t, "v0.0.0-20260812203640-351d2159f8d8", f.Replace[0].New.Version)
	// The require line itself is untouched -- only the replace changes what
	// actually gets built.
	require.Len(t, f.Require, 1)
	assert.Equal(t, "v1.50.1", f.Require[0].Mod.Version)
}

// The bare replace this writes is picked up by EnforceOrgBranchTracking (an
// unmarked replace onto an org module) in the very next step of the same
// phase, so one run leaves the dependency both redirected and tracked.
func TestEnforceSqliteFork_ThenEnforceOrgBranchTrackingMarksIt(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require modernc.org/sqlite v1.50.1
`)

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	_, err := EnforceSqliteFork(mock)
	require.NoError(t, err)
	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "// go-toolchain:auto-branch", suffixFor(t, sqliteForkModule))
}

func TestEnforceSqliteFork_LeavesAnExistingReplaceAlone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		gomod string
	}{
		{"already on the fork", `module test
go 1.21

require modernc.org/sqlite v1.50.1

replace modernc.org/sqlite => github.com/wow-look-at-my/go-sqlite v0.0.0-20200101000000-000000000000
`},
		{"replaced by something else entirely", `module test
go 1.21

require modernc.org/sqlite v1.50.1

replace modernc.org/sqlite => gitlab.com/cznic/sqlite v1.50.1
`},
		{"replaced by a local directory", `module test
go 1.21

require modernc.org/sqlite v1.50.1

replace modernc.org/sqlite => ../my-sqlite
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeGoMod(t, tc.gomod)

			mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

			changed, err := EnforceSqliteFork(mock)
			require.NoError(t, err)
			assert.False(t, changed, "an existing replace is a deliberate choice")
			assert.Empty(t, mock.Calls())
		})
	}
}

func TestEnforceSqliteFork_IgnoresAnIndirectRequire(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require modernc.org/sqlite v1.50.1 // indirect
`)

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceSqliteFork(mock)
	require.NoError(t, err)
	assert.False(t, changed, "an indirect require is not a driver a consumer chose to import")
	assert.Empty(t, mock.Calls())
}

func TestEnforceSqliteFork_NoOpWithoutSqlite(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require github.com/spf13/cobra v1.8.0
`)

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceSqliteFork(mock)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Empty(t, mock.Calls())
}

func TestEnforceSqliteFork_NoOpWithoutAGoMod(t *testing.T) {
	t.Chdir(t.TempDir())

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceSqliteFork(mock)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Empty(t, mock.Calls())
}

func TestNeedsSqliteFork(t *testing.T) {
	for _, tc := range []struct {
		name  string
		gomod string
		want  bool
	}{
		{"bare direct require", `module test
go 1.21

require modernc.org/sqlite v1.50.1
`, true},
		{"already replaced", `module test
go 1.21

require modernc.org/sqlite v1.50.1

replace modernc.org/sqlite => github.com/wow-look-at-my/go-sqlite v0.0.0-20200101000000-000000000000
`, false},
		{"indirect only", `module test
go 1.21

require modernc.org/sqlite v1.50.1 // indirect
`, false},
		{"not a dependency at all", `module test
go 1.21

require github.com/spf13/cobra v1.8.0
`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeGoMod(t, tc.gomod)
			assert.Equal(t, tc.want, needsSqliteFork())
		})
	}
}
