package cmd

// OrgModulePrefixes names the module path prefixes this org publishes, which
// keeps a checksum-database query off them (see orgSumDBExemptions, goenv.go).
var OrgModulePrefixes = []string{"github.com/wow-look-at-my/"}
