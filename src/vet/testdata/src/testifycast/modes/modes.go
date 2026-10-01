// Package modes exists so a fixture file can hold an operand whose type lives
// in a package that file does not import (see notimported.go): the inserted
// conversion must then also add this package's import.
package modes

// Mode mirrors the io/fs.FileMode shape: a named numeric type.
type Mode uint32
