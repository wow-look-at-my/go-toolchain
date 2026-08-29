package jsoninterp

import (
	"fmt"
	htmltemplate "html/template"
	"io"
	"text/template"
)

// A format string is the document, and every verb is a value entering it unescaped.
func objectByFormat(sha string) string {
	return fmt.Sprintf(`{"sha":%q}`, sha) // want `JSON built by formatting`
}

// The writer-first spelling reaches the same document.
func objectToWriter(w io.Writer, name string) {
	fmt.Fprintf(w, `{"name":"%s","ok":true}`, name) // want `JSON built by formatting`
}

// An error body is a document too.
func errorBody(reason string) error {
	return fmt.Errorf(`{"error":"%s"}`, reason) // want `JSON built by formatting`
}

// Appending to a byte slice spells the same JSON.
func appendBody(buf []byte, id int) []byte {
	return fmt.Appendf(buf, `{"id":%d}`, id) // want `JSON built by formatting`
}

// Concatenation reads as one document: the fragments join, holes and all.
func objectByConcat(sha string) string {
	return `{"sha":"` + sha + `"}` // want `JSON built by concatenation`
}

// An array of one quoted value is the shape a quote in the value breaks.
func arrayByConcat(id string) string {
	return `["` + id + `"]` // want `JSON built by concatenation`
}

// A nested concatenation reports once, at the whole document.
func nestedConcat(a, b string) string {
	return `{"a":"` + a + `","b":"` + b + `"}` // want `JSON built by concatenation`
}

// text/template escapes nothing at all.
func textTemplate() (*template.Template, error) {
	return template.New("body").Parse(`{"name":"{{.Name}}"}`) // want `a JSON template`
}

// html/template escapes for HTML, which is a different document.
func htmlTemplate() (*htmltemplate.Template, error) {
	return htmltemplate.New("body").Parse(`{"name":"{{.Name}}"}`) // want `a JSON template`
}

// Static JSON carries no value, so nothing can break it.
const staticBody = `{"ok":true}`

// A constant document is still constant once joined.
const joinedBody = `{"ok":` + `true}`

// Prose that quotes an example is not a document.
func message(got string) string {
	return fmt.Sprintf(`expected {"ok":true}, got %s`, got)
}

// Braces with no string are some other notation.
func braces(v string) string {
	return fmt.Sprintf("{%s}", v)
}

// A bracketed tag is not an array of values.
func tagged(tag, msg string) string {
	return fmt.Sprintf("[%s] %s", tag, msg)
}

// A separator carries no JSON structure.
func joined(name string, n int) string {
	return fmt.Sprintf("%s: %d", name, n)
}

// A template with no action renders one fixed document.
func staticTemplate() (*template.Template, error) {
	return template.New("body").Parse(`{"ok":true}`)
}

// Marshaling is the remedy, and it is never reported.
func marshaled(sha string) any {
	return struct {
		SHA string `json:"sha"`
	}{SHA: sha}
}
