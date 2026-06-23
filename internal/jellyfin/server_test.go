package jellyfin

import (
	"net/http/httptest"
	"testing"

	"github.com/BaksiLi/stashfin/internal/config"
	"github.com/BaksiLi/stashfin/internal/stash"
)

func TestSceneSortKeepsReleaseAndAddedDatesDistinct(t *testing.T) {
	tests := []struct {
		sortBy string
		want   string
	}{
		{sortBy: "PremiereDate", want: "date"},
		{sortBy: "ProductionYear", want: "date"},
		{sortBy: "DateCreated", want: "created_at"},
	}

	for _, tt := range tests {
		t.Run(tt.sortBy, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/Items?SortBy="+tt.sortBy, nil)
			got, _ := sceneSort(request)
			if got != tt.want {
				t.Fatalf("sceneSort(%q) = %q, want %q", tt.sortBy, got, tt.want)
			}
		})
	}
}

func TestFormatItemReturnsIndependentReleaseAndAddedDates(t *testing.T) {
	server := &Server{cfg: config.Config{ServerID: "test-server"}}
	item := server.formatItem(stash.Scene{
		ID:        "42",
		Title:     "Example",
		Date:      "2021-03-14",
		CreatedAt: "2024-08-09T10:11:12Z",
		Files:     []stash.VideoFile{{Duration: 60}},
	}, "root-scenes")

	if got := item["PremiereDate"]; got != "2021-03-14T00:00:00.0000000Z" {
		t.Fatalf("PremiereDate = %#v", got)
	}
	if got := item["DateCreated"]; got != "2024-08-09T10:11:12Z" {
		t.Fatalf("DateCreated = %#v", got)
	}
}
