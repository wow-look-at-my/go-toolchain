package deadcode

// inst has the same shape as shader-simulator's spirv.Inst: the types that
// satisfy it get setSourceLoc only by embedding baseInst.
type inst interface {
	opcode() int
	setSourceLoc(line int)
}

type baseInst struct {
	line int
}

func (b *baseInst) setSourceLoc(line int) {
	b.line = line
}

type opAdd struct {
	baseInst
}

func (o *opAdd) opcode() int { return 1 }

// opWrapped reaches baseInst through a couple of levels of embedding.
type opWrapped struct {
	opAdd
}

func (o *opWrapped) opcode() int { return 2 }

func decode(op int) inst {
	if op == 2 {
		return &opWrapped{}
	}
	return &opAdd{}
}

func parse() {
	i := decode(1)
	i.setSourceLoc(7)
}

var _ = parse

// marked is an interface the package never calls. deepBase alone does not
// implement it.
type marked interface {
	mark()
	kind() int
}

type deepBase struct{}

func (deepBase) mark() {}

type middle struct {
	deepBase
}

type genericHolder[T any] struct {
	middle
	v T
}

func (genericHolder[T]) kind() int { return 3 }

var _ marked = genericHolder[int]{}

// walker satisfies only an anonymous interface, which run calls through.
type walker struct{}

func (walker) step(n int) {}

func run(s interface{ step(int) }) {
	s.step(1)
}

var _ = run
var _ = walker{}

// lone.step has the same name as the interface method above but a different signature, so nothing can reach it.
type lone struct{}

func (lone) step(s string) {} // want "function step is unused within this package"

var _ = lone{}
