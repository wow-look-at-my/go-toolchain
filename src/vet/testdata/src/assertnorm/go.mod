module assertnorm

go 1.24

require github.com/stretchr/testify v1.9.0

// Hermetic: resolve upstream testify to the local stub so fixtures type-check
// with the correct package path and no network dependency.
replace github.com/stretchr/testify => ../testifystub
