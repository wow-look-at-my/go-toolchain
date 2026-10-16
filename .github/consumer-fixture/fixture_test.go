package consumerfixture

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSum(t *testing.T) {
	require.Equal(t, 6, Sum(1, 2, 3))
	require.Equal(t, 0, Sum())
}
