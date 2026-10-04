package main

import "testing"

// The go command refuses a GOPROXY that holds no entry, saying it "is not the
// empty string, but contains no entries". A runner supplies exactly that, and
// reading it as a value to be taken as it is left every consumer's generator
// install dead before the pipeline ran.
func TestNamesAProxy(test *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{",", false},
		{" ", false},
		{",,", false},
		{"|", false},
		{" , ", false},
		{"direct", true},
		{"off", true},
		{"https://proxy.example/mod,direct", true},
		{",direct", true},
		{"https://a|https://b", true},
	}
	for _, each := range cases {
		if got := namesAProxy(each.value); got != each.want {
			test.Errorf("namesAProxy(%q) = %v, want %v", each.value, got, each.want)
		}
	}
}
