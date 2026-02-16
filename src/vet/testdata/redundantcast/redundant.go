package redundantcast

func main() {
	_ = int(0)       // want "redundant cast: int\\(0\\) can be just 0"
	_ = float64(1.5) // want "redundant cast: float64\\(1.5\\) can be just 1.5"
	_ = string("hi") // want `redundant cast: string\("hi"\) can be just "hi"`

	// These are NOT redundant
	_ = int64(0)
	_ = float32(1.5)
	_ = int32('a')
}
