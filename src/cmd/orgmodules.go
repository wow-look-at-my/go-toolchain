package cmd

// OrgModulePrefixes names the module path prefixes this org publishes. A module
// under one of them carries no version of its own, so a checksum-database query
// must never reach it (see orgSumDBExemptions in goenv.go).
var OrgModulePrefixes = []string{"github.com/wow-look-at-my/"}
