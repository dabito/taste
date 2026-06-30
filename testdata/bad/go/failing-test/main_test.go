package failingtest

import "testing"

func TestFails(t *testing.T) {
	t.Fatal("target-root go test fixture")
}
