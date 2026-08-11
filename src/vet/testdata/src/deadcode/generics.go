package deadcode

// box is a minimal generic type whose own methods call each other, the same
// shape as SortedMap.Min calling SortedMap.minNode in ansi-writer's sibling
// repos. usedHelper must NOT warn even though its only caller is another
// method of the same generic type.

type box[T any] struct {
	v T
}

func (b *box[T]) Get() T {
	return b.usedHelper()
}

func (b *box[T]) usedHelper() T {
	return b.v
}

func (b *box[T]) deadHelper() T { // want "function deadHelper is unused within this package"
	return b.v
}

var _ = (&box[int]{}).Get()
