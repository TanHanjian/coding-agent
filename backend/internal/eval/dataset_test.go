package eval

import (
	"strings"
	"testing"
)

func TestLoadCasesRejectsDuplicateAndUnknownVersion(t *testing.T) {
	duplicate := `{"id":"a","version":1,"input":{"query":"q"}}
{"id":"a","version":1,"input":{"query":"q"}}`
	if _, err := LoadCases(strings.NewReader(duplicate)); err == nil {
		t.Fatal("expected duplicate id error")
	}
	unknown := `{"id":"a","version":2,"input":{"query":"q"}}`
	if _, err := LoadCases(strings.NewReader(unknown)); err == nil {
		t.Fatal("expected version error")
	}
}

func TestLoadCasesRejectsUnknownField(t *testing.T) {
	data := `{"id":"a","version":1,"input":{"query":"q"},"unexpected":true}`
	if _, err := LoadCases(strings.NewReader(data)); err == nil {
		t.Fatal("expected unknown field error")
	}
}
