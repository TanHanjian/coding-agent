package interview

import "testing"

func TestNewBuilderRequiresModel(t *testing.T) {
	builder, err := NewBuilder(nil)
	if err == nil {
		t.Fatalf("expected validation error, got builder=%v", builder)
	}
}
