package deadcode

import "fmt"

// --- Unused symbols: should trigger warnings ---

func unusedFunc() {} // want "function unusedFunc is unused within this package"

type unusedType struct{} // want "type unusedType is unused within this package"

const unusedConst = 42 // want "const unusedConst is unused within this package"

var unusedVar = "hello" // want "var unusedVar is unused within this package"

// --- Used symbols: should NOT trigger warnings ---

func usedFunc() string { return "used" }

type usedType struct{}

const usedConst = 1

var usedVar = "world"

// --- Special functions: should NOT trigger warnings ---

func init() {
	// init is always implicitly called.
	_ = usedFunc()
	_ = usedType{}
	_ = usedConst
	_ = usedVar
}

// --- Blank identifiers: should NOT trigger warnings ---

var _ = 0

// --- Exported symbols: should NOT trigger warnings ---

func ExportedFunc() {}

type ExportedType struct{}

const ExportedConst = 1

var ExportedVar = 0

// --- Interface implementation: should NOT trigger warnings ---

type myStringer struct{}

func (m myStringer) String() string { return "" }

var _ fmt.Stringer = myStringer{}
