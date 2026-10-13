package vet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A method that a type gets through an embedded field is in that type's
// method set. When the type satisfies an interface that names the method, a
// call through the interface uses the method. This happens although the base
// type alone does not implement the interface.
func TestDeadCodeKeepsAMethodPromotedIntoAnInterfaceImplementation(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	write("go.mod", "module testmod\n\ngo 1.21\n")
	write("lib.go", `package lib

type Inst interface {
	Opcode() string
	setLoc(loc int)
}

type baseInst struct{ loc int }

func (b *baseInst) setLoc(loc int) { b.loc = loc }

func (b *baseInst) unusedHelper() int { return b.loc }

type addInst struct{ baseInst }

func (a *addInst) Opcode() string { return "add" }

func Parse() []Inst {
	var inst Inst = &addInst{}
	inst.setLoc(3)
	return []Inst{inst}
}
`)

	t.Chdir(dir)

	_, err := RunOnPattern("./...", false, nil)

	require.Error(t, err, "unusedHelper is in no interface and nothing calls it")
	msg := err.Error()
	assert.NotContains(t, msg, "setLoc")
	assert.Contains(t, msg, "unusedHelper")
}
