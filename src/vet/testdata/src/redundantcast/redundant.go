package redundantcast

func main() {
	_ = int(0)       // want "redundant cast: int\\(0\\) can be just 0"
	_ = float64(1.5) // want "redundant cast: float64\\(1.5\\) can be just 1.5"
	_ = string("hi") // want `redundant cast: string\("hi"\) can be just "hi"`
	_ = rune('x')    // want "redundant cast: rune\\('x'\\) can be just 'x'"

	// These are NOT redundant
	_ = int64(0)
	_ = float32(1.5)
	_ = int32('a') // want "redundant cast: int32\\('a'\\) can be just 'a'"
	_ = byte('b')  // NOT redundant - byte is uint8, not rune

	// Multiple in one file
	_ = int(42)      // want "redundant cast: int\\(42\\) can be just 42"
	_ = float64(3.0) // want "redundant cast: float64\\(3.0\\) can be just 3.0"
}
