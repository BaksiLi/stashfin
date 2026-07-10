package jellyfin

import "testing"

func TestFetchWindowSupportsArbitraryOffsets(t *testing.T) {
	all := make([]int, 500)
	for i := range all {
		all[i] = i
	}
	fetch := func(page, perPage int) ([]int, int, error) {
		start := (page - 1) * perPage
		if start >= len(all) {
			return []int{}, len(all), nil
		}
		end := start + perPage
		if end > len(all) {
			end = len(all)
		}
		return all[start:end], len(all), nil
	}

	for _, tt := range []struct {
		start int
		limit int
		first int
		last  int
	}{
		{start: 25, limit: 50, first: 25, last: 74},
		{start: 190, limit: 30, first: 190, last: 219},
		{start: 450, limit: 30, first: 450, last: 479},
	} {
		items, total, err := fetchWindow(tt.start, tt.limit, tt.limit, fetch)
		if err != nil {
			t.Fatal(err)
		}
		if total != len(all) || len(items) != tt.limit || items[0] != tt.first || items[len(items)-1] != tt.last {
			t.Fatalf("window start=%d limit=%d: total=%d items=%v", tt.start, tt.limit, total, items)
		}
	}
}
