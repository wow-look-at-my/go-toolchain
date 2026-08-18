package cosmocompat

// libcGap closes modernc.org/libc's cosmo build gap: every file below is an
// exact copy of an existing linux/amd64 or linux/arm64 file (verified
// content-identical apart from the build tag when this table was built)
// with its build tag forced to "cosmo".
var libcGap = gap{
	module:          "modernc.org/libc",
	verifiedVersion: "v1.70.0",
	copies: []copySpec{
		{"errno/errno_linux_amd64.go", "errno/errno_cosmo_amd64.go", ""},
		{"errno/capi_linux_amd64.go", "errno/capi_cosmo_amd64.go", ""},
		{"errno/errno_linux_arm64.go", "errno/errno_cosmo_arm64.go", ""},
		{"errno/capi_linux_arm64.go", "errno/capi_cosmo_arm64.go", ""},
		{"grp/grp_linux_amd64.go", "grp/grp_cosmo_amd64.go", ""},
		{"grp/capi_linux_amd64.go", "grp/capi_cosmo_amd64.go", ""},
		{"grp/grp_linux_arm64.go", "grp/grp_cosmo_arm64.go", ""},
		{"grp/capi_linux_arm64.go", "grp/capi_cosmo_arm64.go", ""},
		{"limits/limits_linux_amd64.go", "limits/limits_cosmo_amd64.go", ""},
		{"limits/capi_linux_amd64.go", "limits/capi_cosmo_amd64.go", ""},
		{"limits/limits_linux_arm64.go", "limits/limits_cosmo_arm64.go", ""},
		{"limits/capi_linux_arm64.go", "limits/capi_cosmo_arm64.go", ""},
		{"poll/poll_linux_amd64.go", "poll/poll_cosmo_amd64.go", ""},
		{"poll/capi_linux_amd64.go", "poll/capi_cosmo_amd64.go", ""},
		{"poll/poll_linux_arm64.go", "poll/poll_cosmo_arm64.go", ""},
		{"poll/capi_linux_arm64.go", "poll/capi_cosmo_arm64.go", ""},
		{"pthread/pthread_linux_amd64.go", "pthread/pthread_cosmo_amd64.go", ""},
		{"pthread/capi_linux_amd64.go", "pthread/capi_cosmo_amd64.go", ""},
		{"pthread/pthread_linux_arm64.go", "pthread/pthread_cosmo_arm64.go", ""},
		{"pthread/capi_linux_arm64.go", "pthread/capi_cosmo_arm64.go", ""},
		{"pwd/pwd_linux_amd64.go", "pwd/pwd_cosmo_amd64.go", ""},
		{"pwd/capi_linux_amd64.go", "pwd/capi_cosmo_amd64.go", ""},
		{"pwd/pwd_linux_arm64.go", "pwd/pwd_cosmo_arm64.go", ""},
		{"pwd/capi_linux_arm64.go", "pwd/capi_cosmo_arm64.go", ""},
		{"signal/more_linux_amd64.go", "signal/more_cosmo_amd64.go", ""},
		{"signal/signal_linux_amd64.go", "signal/signal_cosmo_amd64.go", ""},
		{"signal/capi_linux_amd64.go", "signal/capi_cosmo_amd64.go", ""},
		{"signal/more_linux_arm64.go", "signal/more_cosmo_arm64.go", ""},
		{"signal/signal_linux_arm64.go", "signal/signal_cosmo_arm64.go", ""},
		{"signal/capi_linux_arm64.go", "signal/capi_cosmo_arm64.go", ""},
		{"stdio/stdio_linux_amd64.go", "stdio/stdio_cosmo_amd64.go", ""},
		{"stdio/capi_linux_amd64.go", "stdio/capi_cosmo_amd64.go", ""},
		{"stdio/stdio_linux_arm64.go", "stdio/stdio_cosmo_arm64.go", ""},
		{"stdio/capi_linux_arm64.go", "stdio/capi_cosmo_arm64.go", ""},
		{"stdlib/stdlib_linux_amd64.go", "stdlib/stdlib_cosmo_amd64.go", ""},
		{"stdlib/capi_linux_amd64.go", "stdlib/capi_cosmo_amd64.go", ""},
		{"stdlib/stdlib_linux_arm64.go", "stdlib/stdlib_cosmo_arm64.go", ""},
		{"stdlib/capi_linux_arm64.go", "stdlib/capi_cosmo_arm64.go", ""},
		{"sys/types/types_linux_amd64.go", "sys/types/types_cosmo_amd64.go", ""},
		{"sys/types/capi_linux_amd64.go", "sys/types/capi_cosmo_amd64.go", ""},
		{"sys/types/types_linux_arm64.go", "sys/types/types_cosmo_arm64.go", ""},
		{"sys/types/capi_linux_arm64.go", "sys/types/capi_cosmo_arm64.go", ""},
		{"time/time_linux_amd64.go", "time/time_cosmo_amd64.go", ""},
		{"time/capi_linux_amd64.go", "time/capi_cosmo_amd64.go", ""},
		{"time/time_linux_arm64.go", "time/time_cosmo_arm64.go", ""},
		{"time/capi_linux_arm64.go", "time/capi_cosmo_arm64.go", ""},
		{"unistd/unistd_linux_amd64.go", "unistd/unistd_cosmo_amd64.go", ""},
		{"unistd/capi_linux_amd64.go", "unistd/capi_cosmo_amd64.go", ""},
		{"unistd/unistd_linux_arm64.go", "unistd/unistd_cosmo_arm64.go", ""},
		{"unistd/capi_linux_arm64.go", "unistd/capi_cosmo_arm64.go", ""},
		{"uuid/uuid/uuid_linux_amd64.go", "uuid/uuid/uuid_cosmo_amd64.go", ""},
		{"uuid/uuid/capi_linux_amd64.go", "uuid/uuid/capi_cosmo_amd64.go", ""},
		{"uuid/uuid/uuid_linux_arm64.go", "uuid/uuid/uuid_cosmo_arm64.go", ""},
		{"uuid/uuid/capi_linux_arm64.go", "uuid/uuid/capi_cosmo_arm64.go", ""},

		// The ABI0-calling-convention wrapper trampolines qbecc emits are an
		// amd64-only mechanism (arm64's ccgo output never calls the
		// Y-prefixed wrappers), so there is no arm64 counterpart to add.
		{"abi0_linux_amd64.go", "abi0_cosmo_amd64.go", ""},
		{"abi0_linux_amd64.s", "abi0_cosmo_amd64.s", ""},

		{"capi_linux_amd64.go", "capi2_cosmo_amd64.go", ""},
		{"capi_linux_arm64.go", "capi2_cosmo_arm64.go", ""},
		{"ccgo_linux_amd64.go", "ccgo_cosmo_amd64.go", ""},
		{"ccgo_linux_arm64.go", "ccgo_cosmo_arm64.go", ""},
		{"libc_musl_linux_amd64.go", "libc_musl_cosmo_amd64.go", ""},
		{"libc_musl_linux_arm64.go", "libc_musl_cosmo_arm64.go", ""},

		// These six are each already multi-arch (tagged for every linux
		// GOARCH the module supports), so one cosmo copy of each covers
		// both amd64 and arm64.
		{"libc_musl.go", "libc_cosmo_musl.go", ""},
		{"pthread_musl.go", "pthread_cosmo_musl.go", ""},
		{"etc_musl.go", "etc_cosmo_musl.go", ""},
		{"mem_musl.go", "mem_cosmo_musl.go", ""},
		{"builtin.go", "builtin_cosmo.go", ""},
		{"syscall_musl.go", "syscall_cosmo_musl.go", ""},
		{"rtl.go", "rtl_cosmo.go", ""},
		{"atomic.go", "atomic_cosmo.go", ""},
		{"atomic64.go", "atomic64_cosmo.go", ""},
	},
	// tagEdits: files whose existing negative build tag ("everything except
	// linux/amd64", "everything except linux", and similar) is also true
	// under GOOS=cosmo, so it collides with one of the copies above
	// declaring the same symbols for cosmo.
	tagEdits: []tagEdit{
		{"ccgo.go"},
		{"etc.go"},
		{"ioutil_linux.go"},
		{"libc.go"},
		{"libc64.go"},
		{"libc_amd64.go"},
		{"libc_arm64.go"},
		{"libc_linux_amd64.go"},
		{"libc_unix.go"},
		{"libc_unix1.go"},
		{"libc_unix3.go"},
		{"mem.go"},
		{"mem_brk.go"},
		{"memgrind.go"},
		{"printf.go"},
		{"pthread.go"},
		{"pthread_all.go"},
		{"scanf.go"},
		{"sync.go"},
	},
	overlays: map[string]string{
		"tls_cosmo_amd64.go": "overlay/libc_tls_cosmo_amd64.go.tmpl",
	},
}
