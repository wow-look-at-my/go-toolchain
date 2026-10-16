// Package consumerfixture is the module the consumer job builds with the action, as a repository that uses go-toolchain would.
package consumerfixture

// Sum answers the sum of values.
func Sum(values ...int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}
