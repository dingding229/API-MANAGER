package observability

import "testing"

func TestBoundedWindowPreservesRetentionAndOrder(t *testing.T) {
	var items []int
	for i := 0; i < 10000; i++ {
		items = appendBounded(items, i, 250)
		expected := i + 1
		if expected > 250 {
			expected = 250
		}
		if len(items) != expected || items[len(items)-1] != i || items[0] != i-expected+1 {
			t.Fatalf("retention/order changed at %d", i)
		}
	}
	if got := appendBounded([]int{1, 2}, 3, 0); len(got) != 2 {
		t.Fatal("disabled retention changed")
	}
	items = appendBounded([]int{1, 2, 3, 4}, 5, 2)
	if len(items) != 2 || items[0] != 4 || items[1] != 5 {
		t.Fatal("shrinking retention changed")
	}
}
func BenchmarkBoundedLogWindow(b *testing.B) {
	for _, mode := range []string{"shift", "window"} {
		b.Run(mode, func(b *testing.B) {
			entries := make([]LogEntry, 3000, 4000)
			entry := LogEntry{Message: "benchmark"}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if mode == "shift" {
					copy(entries, entries[1:])
					entries = entries[:2999]
					entries = append(entries, entry)
				} else {
					entries = appendBounded(entries, entry, 3000)
				}
			}
		})
	}
}
