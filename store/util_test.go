package store

import (
	"testing"

	"github.com/pkg/errors"
)

func TestInvalidateName(t *testing.T) {
	s := "FooBar"
	err := invalidateName{errors.Errorf("invalid name %s, name needs to start with a letter and may not contain symbols, except ._-", s)}
	if !errors.Is(err, InvalidateName) {
		t.Fatalf("Invalidate name not detected: %s", err)
	}
}
