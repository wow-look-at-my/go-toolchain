package deadcode

// crossFileUsed is defined here but used in deadcode.go via init().
// This file tests that cross-file references within the same package work.

// usedFromHere is used only in this file — should NOT warn.
func usedFromHere() int { return crossFileHelper() }

// crossFileHelper is called by usedFromHere above — should NOT warn.
func crossFileHelper() int { return 0 }

// unusedInUsage is defined here and never referenced anywhere.
func unusedInUsage() {} // want "function unusedInUsage is unused within this package"

var _ = usedFromHere()
