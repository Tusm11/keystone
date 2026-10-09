package codegen

import (
	"regexp"
	"testing"
)

// Run with: go test ./internal/primitives/codegen -v

func TestRandom_Length(t *testing.T) {
	tests := []int{1, 6, 7, 12, 32}
	for _, length := range tests {
		got, err := Random(length)
		if err != nil {
			t.Fatalf("Random(%d) returned error: %v", length, err)
		}
		if len(got) != length {
			t.Errorf("Random(%d) length = %d, want %d", length, len(got), length)
		}
	}
}

func TestRandom_DefaultsOnNonPositive(t *testing.T) {
	// Length ≤ 0 should produce a 7-char code (documented default).
	for _, length := range []int{0, -1, -100} {
		got, err := Random(length)
		if err != nil {
			t.Fatalf("Random(%d) returned error: %v", length, err)
		}
		if len(got) != 7 {
			t.Errorf("Random(%d) length = %d, want 7 (default)", length, len(got))
		}
	}
}

func TestRandom_AlphabetOnly(t *testing.T) {
	// Every character must be in the base62 alphabet.
	re := regexp.MustCompile(`^[A-Za-z0-9]+$`)
	for i := 0; i < 100; i++ {
		got, err := Random(10)
		if err != nil {
			t.Fatalf("Random returned error: %v", err)
		}
		if !re.MatchString(got) {
			t.Errorf("Random produced out-of-alphabet char: %q", got)
		}
	}
}

func TestRandom_Uniqueness(t *testing.T) {
	// 1000 7-char codes should all be distinct (62^7 ≈ 3.5e12 collision space).
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		code, err := Random(7)
		if err != nil {
			t.Fatalf("Random returned error: %v", err)
		}
		if _, dup := seen[code]; dup {
			t.Errorf("duplicate code produced in 1000 iterations: %q", code)
		}
		seen[code] = struct{}{}
	}
}
