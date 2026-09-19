package cmd

// OrgModulePrefixes names the module path prefixes this org publishes. A module
// under any of them carries no version of its own: the go command resolves it
// to the head of a branch, and neither go.mod nor go.sum records a commit for
// it. The list is what keeps a checksum-database query off an org module path
// (see orgSumDBExemptions in goenv.go).
var OrgModulePrefixes = []string{"github.com/wow-look-at-my/"}
