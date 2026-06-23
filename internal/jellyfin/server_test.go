package jellyfin

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/BaksiLi/stashfin/internal/buildinfo"
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

func TestPerformerLibraryAndCastUseDistinctJellyfinIdentities(t *testing.T) {
	server := &Server{cfg: config.Config{ServerID: "test-server"}}
	performer := stash.Performer{ID: "42", Name: "Example", SceneCount: 3}

	folder := server.formatPerformerFolder(performer, "root-performers")
	person := server.formatPerformer(performer, "")

	if got := folder["Id"]; got != "11000000-0000-0000-0000-000000000042" {
		t.Fatalf("performer folder Id = %#v", got)
	}
	if got := folder["Type"]; got != "Folder" {
		t.Fatalf("performer folder Type = %#v", got)
	}
	if got := person["Id"]; got != "10000000-0000-0000-0000-000000000042" {
		t.Fatalf("cast person Id = %#v", got)
	}
	if got := person["Type"]; got != "Person" {
		t.Fatalf("cast person Type = %#v", got)
	}
	if got := person["IsFolder"]; got != false {
		t.Fatalf("cast person IsFolder = %#v", got)
	}
	if _, ok := person["CollectionType"]; ok {
		t.Fatal("cast person must not include CollectionType")
	}
}

func TestPerformerFolderUUIDRoundTrip(t *testing.T) {
	id := entityUUID("performer", "42")
	if got := uuidKind(id); got != "performer" {
		t.Fatalf("uuidKind(%q) = %q", id, got)
	}
	if got := prefixedIDFromUUID(id); got != "performer-42" {
		t.Fatalf("prefixedIDFromUUID(%q) = %q", id, got)
	}
}

func TestHealthReportsStashfinBuild(t *testing.T) {
	previousVersion, previousCommit := buildinfo.Version, buildinfo.Commit
	buildinfo.Version, buildinfo.Commit = "v1.2.3", "abc123"
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Commit = previousVersion, previousCommit
	})

	server := NewServer(config.Config{}, nil, nil)
	request := httptest.NewRequest("GET", "/healthz", nil)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("X-Stashfin-Version"); got != "v1.2.3" {
		t.Fatalf("X-Stashfin-Version = %q", got)
	}
	var response map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["version"] != "v1.2.3" || response["commit"] != "abc123" {
		t.Fatalf("health response = %#v", response)
	}
}
