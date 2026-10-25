package ports

import "testing"

func TestTakePrefersFreePort(t *testing.T) {
	var pool Pool
	first, err := pool.Take(0)
	if err != nil || first == 0 {
		t.Fatalf("take: %v %d", err, first)
	}
	second, err := pool.Take(first)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("reserved port %d was issued twice", first)
	}
}
