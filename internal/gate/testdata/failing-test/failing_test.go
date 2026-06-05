package failingtest

import "testing"

func TestIntentionalFailure(t *testing.T) {
	t.Fatal("intentional failure from fixture")
}
