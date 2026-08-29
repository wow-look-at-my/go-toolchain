package vet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsJSONDocument pins the classifier the whole check rests on: what counts
// as a JSON document, and what is ordinary text that happens to hold a brace.
func TestIsJSONDocument(t *testing.T) {
	for _, c := range []struct {
		name string
		text string
		want bool
	}{
		{"object with a key", "{\"sha\":\x00}", true},
		{"object over several lines", "{\n  \"sha\": \x00\n}", true},
		{"array of strings", "[\"\x00\"]", true},
		{"array of objects", "[{\x00}]", true},
		{"a fragment ending inside a string", "{\"sha\":\"", true},
		{"literals and numbers", "{\"ok\":true,\"n\":-1.5e3,\"v\":null}", true},
		{"prose quoting an example", "expected {\"ok\":true}, got \x00", false},
		{"braces with no string", "{\x00}", false},
		{"array of bare values", "[\x00]", false},
		{"a css rule", ".btn{color:\x00}", false},
		{"a shell expansion", "${\x00}", false},
		{"a separator", "\x00: \x00", false},
		{"empty", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, isJSONDocument(c.text))
		})
	}
}

// TestNormalizeVerbs pins what counts as a value entering a format string. A
// doubled percent sign prints one and interpolates nothing.
func TestNormalizeVerbs(t *testing.T) {
	for _, c := range []struct {
		name  string
		text  string
		want  string
		verbs int
	}{
		{"one verb", "{\"a\":%s}", "{\"a\":\x00}", 1},
		{"quoted verb", "{\"a\":%q}", "{\"a\":\x00}", 1},
		{"width and precision", "%8.2f", "\x00", 1},
		{"argument index", "%[2]s", "\x00", 1},
		{"doubled percent", "100%% done", "100 done", 0},
		{"trailing percent", "50%", "50%", 0},
		{"no verb", "{\"a\":1}", "{\"a\":1}", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			text, verbs := normalizeVerbs(c.text)
			assert.Equal(t, c.want, text)
			assert.Equal(t, c.verbs, verbs)
		})
	}
}

// TestNormalizeActions pins that a template's actions are the values, and an
// action the text never closes ends it.
func TestNormalizeActions(t *testing.T) {
	text, actions := normalizeActions("{\"a\":\"{{.A}}\",\"b\":\"{{.B}}\"}")
	assert.Equal(t, "{\"a\":\"\x00\",\"b\":\"\x00\"}", text)
	require.Equal(t, 2, actions)

	text, actions = normalizeActions("{\"a\":\"{{.A")
	assert.Equal(t, "{\"a\":\"", text)
	assert.Equal(t, 0, actions)
}
