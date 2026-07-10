package jellyfin

type pageFetcher[T any] func(page, perPage int) ([]T, int, error)

func fetchWindow[T any](start, limit, pageSize int, fetch pageFetcher[T]) ([]T, int, error) {
	if limit < 1 {
		return []T{}, 0, nil
	}
	if pageSize < limit {
		pageSize = limit
	}
	if pageSize < 1 {
		pageSize = limit
	}

	page := start/pageSize + 1
	offset := start % pageSize
	items, total, err := fetch(page, pageSize)
	if err != nil {
		return nil, 0, err
	}

	window := make([]T, 0, limit)
	if offset < len(items) {
		window = append(window, items[offset:]...)
	}
	if len(window) < limit && page*pageSize < total {
		next, _, err := fetch(page+1, pageSize)
		if err != nil {
			return nil, 0, err
		}
		window = append(window, next...)
	}
	if len(window) > limit {
		window = window[:limit]
	}
	return window, total, nil
}
