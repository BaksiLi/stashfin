package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BaksiLi/stashfin/internal/buildinfo"
	"github.com/BaksiLi/stashfin/internal/config"
	"github.com/BaksiLi/stashfin/internal/stash"
)

type Server struct {
	cfg        config.Config
	stash      *stash.Client
	log        *slog.Logger
	playback   *playbackTracker
	imageCache sync.Map
	wallCache  sync.Map
	imageSeed  string
}

type wallCacheEntry struct {
	data     []byte
	cachedAt time.Time
}

const (
	maxCoverImageBytes  = 16 << 20
	maxCoverImagePixels = 12_000_000
	maxCoverImageSide   = 16_384
)

func NewServer(cfg config.Config, stashClient *stash.Client, logger *slog.Logger) *Server {
	return &Server{
		cfg:       cfg,
		stash:     stashClient,
		log:       logger,
		playback:  newPlaybackTracker(),
		imageSeed: strconv.FormatInt(time.Now().Unix(), 36),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cleanPath := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	lowerPath := strings.ToLower(cleanPath)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Emby-Authorization, X-Emby-Token, X-MediaBrowser-Token")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("X-Stashfin-Version", buildinfo.Version)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if lowerPath == "/" {
		http.Redirect(w, r, "/System/Info/Public", http.StatusFound)
		return
	}
	if lowerPath == "/healthz" {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"version": buildinfo.Version,
			"commit":  buildinfo.Commit,
		})
		return
	}

	if isPublic(lowerPath) {
		s.servePublic(w, r, lowerPath)
		return
	}

	if !s.validToken(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		return
	}

	switch {
	case lowerPath == "/userviews":
		s.userViews(w, r)
	case lowerPath == "/userviews/groupingoptions":
		writeJSON(w, http.StatusOK, []map[string]any{})
	case lowerPath == "/library/virtualfolders":
		s.virtualFolders(w, r)
	case lowerPath == strings.ToLower("/Users/"+s.cfg.User):
		s.userProfile(w, r)
	case lowerPath == strings.ToLower("/Users/"+s.cfg.User+"/Views"):
		s.userViews(w, r)
	case strings.HasPrefix(lowerPath, "/users/") && strings.HasSuffix(lowerPath, "/groupingoptions"):
		writeJSON(w, http.StatusOK, []map[string]any{})
	case lowerPath == "/items":
		s.items(w, r)
	case lowerPath == "/genres":
		s.genres(w, r)
	case strings.HasPrefix(lowerPath, "/genres/") && strings.Contains(lowerPath, "/images/"):
		s.genreImage(w, r, pathSegment(cleanPath, 1))
	case strings.HasPrefix(lowerPath, "/genres/"):
		s.genre(w, r, pathSegment(cleanPath, 1))
	case lowerPath == "/persons":
		s.persons(w, r)
	case strings.HasPrefix(lowerPath, "/persons/") && strings.Contains(lowerPath, "/images/"):
		s.personImage(w, r, pathSegment(cleanPath, 1))
	case strings.HasPrefix(lowerPath, "/persons/"):
		s.person(w, r, pathSegment(cleanPath, 1))
	case lowerPath == "/items/latest":
		s.latest(w, r)
	case strings.HasPrefix(lowerPath, "/users/") && strings.Contains(lowerPath, "/items/latest"):
		s.latest(w, r)
	case strings.HasPrefix(lowerPath, "/users/") && strings.Contains(lowerPath, "/items/resume"):
		writeJSON(w, http.StatusOK, itemsResponse(nil, 0, 0))
	case strings.HasPrefix(lowerPath, "/users/") && strings.HasSuffix(lowerPath, "/items"):
		s.items(w, r)
	case strings.HasPrefix(lowerPath, "/users/") && strings.Contains(lowerPath, "/items/"):
		s.itemByPath(w, r, cleanPath)
	case strings.HasPrefix(lowerPath, "/useritems/") && strings.HasSuffix(lowerPath, "/rating"):
		s.userItemRating(w, r, pathSegment(cleanPath, 1))
	case strings.HasPrefix(lowerPath, "/userfavoriteitems/"):
		s.userFavorite(w, r, pathSegment(cleanPath, 1))
	case strings.HasPrefix(lowerPath, "/items/") && strings.HasSuffix(lowerPath, "/playbackinfo"):
		s.playbackInfo(w, r, itemIDFromPlaybackPath(cleanPath))
	case strings.HasPrefix(lowerPath, "/items/") && strings.Contains(lowerPath, "/images/"):
		s.image(w, r, itemIDFromImagePath(cleanPath))
	case strings.HasPrefix(lowerPath, "/items/"):
		s.item(w, r, pathSegment(cleanPath, 1))
	case strings.HasPrefix(lowerPath, "/videos/") && (strings.HasSuffix(lowerPath, "/stream") || strings.HasSuffix(lowerPath, "/stream.mp4")):
		s.redirectStream(w, r, itemIDFromVideoPath(cleanPath))
	case strings.HasPrefix(lowerPath, "/sessions/playing"):
		s.playing(w, r, lowerPath)
	case strings.HasPrefix(lowerPath, "/sessions/"):
		writeJSON(w, http.StatusOK, map[string]any{})
	default:
		s.log.Debug("unhandled endpoint", "method", r.Method, "path", cleanPath)
		writeJSON(w, http.StatusOK, itemsResponse(nil, 0, 0))
	}
}

func (s *Server) servePublic(w http.ResponseWriter, r *http.Request, p string) {
	switch p {
	case "/system/info", "/system/info/public":
		writeJSON(w, http.StatusOK, map[string]any{
			"LocalAddress":             "http://" + r.Host,
			"ServerName":               s.cfg.ServerName,
			"Version":                  "10.10.0",
			"Id":                       s.cfg.ServerID,
			"ProductName":              "Jellyfin Server",
			"OperatingSystem":          "Linux",
			"StartupWizardCompleted":   true,
			"SupportsLibraryMonitor":   false,
			"WebSocketPortNumber":      8096,
			"CompletedInstallations":   []map[string]string{{"Guid": s.cfg.ServerID, "Name": s.cfg.ServerName}},
			"CanSelfRestart":           false,
			"CanLaunchWebBrowser":      false,
			"ProgramDataPath":          "",
			"ItemsByNamePath":          "",
			"CachePath":                "",
			"LogPath":                  "",
			"InternalMetadataPath":     "",
			"TranscodingTempPath":      "",
			"ServerNameLastModifiedBy": "",
		})
	case "/system/ping":
		w.WriteHeader(http.StatusNoContent)
	case "/branding/configuration":
		writeJSON(w, http.StatusOK, map[string]any{
			"LoginDisclaimer":     "",
			"CustomCss":           "",
			"SplashscreenEnabled": false,
		})
	case "/users":
		writeJSON(w, http.StatusOK, []map[string]any{{
			"Name":                  s.cfg.User,
			"Id":                    s.cfg.User,
			"HasPassword":           true,
			"HasConfiguredPassword": true,
			"Policy":                map[string]any{"IsAdministrator": true},
		}})
	case "/users/authenticatebyname":
		s.authenticate(w, r)
	default:
		writeJSON(w, http.StatusOK, map[string]any{})
	}
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) {
	// Jellyfin clients require a username field, but Stashfin intentionally uses
	// one password credential and a stable configured display name.
	var req struct {
		Username    string `json:"Username"`
		Password    string `json:"Pw"`
		PasswordAlt string `json:"Password"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}

	password := firstNonEmpty(req.Password, req.PasswordAlt)
	if !(s.cfg.AllowEmptyPassword && strings.TrimSpace(s.cfg.Password) == "") && strings.TrimSpace(password) != strings.TrimSpace(s.cfg.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid username or password"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"User": map[string]any{
			"Name":                  s.cfg.User,
			"Id":                    s.cfg.User,
			"HasPassword":           true,
			"HasConfiguredPassword": true,
			"ServerId":              s.cfg.ServerID,
			"PrimaryImageTag":       "",
			"Policy":                map[string]any{"IsAdministrator": true, "EnableMediaPlayback": true, "EnableRemoteAccess": true},
		},
		"SessionInfo": map[string]any{
			"UserId":   s.cfg.User,
			"IsActive": true,
		},
		"AccessToken": s.cfg.AccessToken,
		"ServerId":    s.cfg.ServerID,
	})
}

func (s *Server) userProfile(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"Name":                      s.cfg.User,
		"Id":                        s.cfg.User,
		"HasPassword":               true,
		"HasConfiguredPassword":     true,
		"HasConfiguredEasyPassword": false,
		"EnableAutoLogin":           false,
		"Policy": map[string]any{
			"IsAdministrator":                true,
			"IsHidden":                       false,
			"IsDisabled":                     false,
			"EnableMediaPlayback":            true,
			"EnableRemoteAccess":             true,
			"EnableContentDeletion":          false,
			"EnableVideoPlaybackTranscoding": true,
			"EnableAudioPlaybackTranscoding": true,
		},
		"Configuration": map[string]any{
			"PlayDefaultAudioTrack":      true,
			"SubtitleLanguagePreference": "",
			"SubtitleMode":               "Default",
			"EnableNextEpisodeAutoPlay":  false,
		},
	})
}

func (s *Server) userViews(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"Items":            s.rootFolders(),
		"TotalRecordCount": len(s.rootFolders()),
	})
}

func (s *Server) virtualFolders(w http.ResponseWriter, r *http.Request) {
	folders := []map[string]any{}
	for _, folder := range s.rootFolders() {
		folders = append(folders, map[string]any{
			"Name":           folder["Name"],
			"Locations":      []string{},
			"CollectionType": "movies",
			"ItemId":         folder["Id"],
		})
	}
	writeJSON(w, http.StatusOK, folders)
}

func (s *Server) rootFolders() []map[string]any {
	return []map[string]any{
		s.rootFolder("Scenes", "root-scenes"),
		s.rootFolder("Performers", "root-performers"),
		s.rootFolder("Studios", "root-studios"),
		s.rootFolder("Tags", "root-tags"),
	}
}

func (s *Server) rootFolder(name, id string) map[string]any {
	imageTag := s.rootImageTag(id)
	return map[string]any{
		"Name":            name,
		"Id":              id,
		"ServerId":        s.cfg.ServerID,
		"Type":            "CollectionFolder",
		"IsFolder":        true,
		"CollectionType":  "movies",
		"ImageTags":       map[string]string{"Primary": imageTag},
		"PrimaryImageTag": imageTag,
	}
}

func (s *Server) items(w http.ResponseWriter, r *http.Request) {
	parentID := queryValue(r, "ParentId")
	filter := s.sceneFilterFromQuery(r)
	if filter.PerformerID != "" || filter.StudioID != "" || filter.TagID != "" {
		s.sceneItems(w, r, firstNonEmpty(parentID, "root-scenes"), filter)
		return
	}

	if parentID == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"Items":            s.rootFolders(),
			"TotalRecordCount": len(s.rootFolders()),
			"StartIndex":       startIndex(r),
		})
		return
	}

	switch {
	case parentID == "root-scenes":
		filter.SearchTerm = queryValue(r, "SearchTerm")
		s.sceneItems(w, r, "root-scenes", filter)
	case parentID == "root-performers":
		s.performerItems(w, r)
	case parentID == "root-studios":
		s.studioItems(w, r)
	case parentID == "root-tags":
		s.tagItems(w, r)
	case strings.HasPrefix(parentID, "person-"):
		filter.PerformerID = numericEntityID(parentID, "person-")
		filter.SearchTerm = queryValue(r, "SearchTerm")
		s.sceneItems(w, r, parentID, filter)
	case uuidKind(parentID) == "person":
		filter.PerformerID = numericEntityRef(parentID, "person")
		filter.SearchTerm = queryValue(r, "SearchTerm")
		s.sceneItems(w, r, parentID, filter)
	case strings.HasPrefix(parentID, "performer-"):
		filter.PerformerID = numericEntityID(parentID, "performer-")
		filter.SearchTerm = queryValue(r, "SearchTerm")
		s.sceneItems(w, r, parentID, filter)
	case uuidKind(parentID) == "performer":
		filter.PerformerID = numericEntityRef(parentID, "performer")
		filter.SearchTerm = queryValue(r, "SearchTerm")
		s.sceneItems(w, r, parentID, filter)
	case strings.HasPrefix(parentID, "studio-"):
		filter.StudioID = numericEntityID(parentID, "studio-")
		filter.SearchTerm = queryValue(r, "SearchTerm")
		s.sceneItems(w, r, parentID, filter)
	case uuidKind(parentID) == "studio":
		filter.StudioID = numericEntityRef(parentID, "studio")
		filter.SearchTerm = queryValue(r, "SearchTerm")
		s.sceneItems(w, r, parentID, filter)
	case strings.HasPrefix(parentID, "tag-"):
		s.tagChildrenOrScenes(w, r, numericEntityID(parentID, "tag-"), parentID)
	case strings.HasPrefix(parentID, "tagall-"):
		filter.TagID = numericEntityID(parentID, "tagall-")
		filter.SearchTerm = queryValue(r, "SearchTerm")
		s.sceneItems(w, r, parentID, filter)
	case uuidKind(parentID) == "tag":
		s.tagChildrenOrScenes(w, r, numericEntityRef(parentID, "tag"), parentID)
	default:
		writeJSON(w, http.StatusOK, itemsResponse(nil, startIndex(r), 0))
	}
}

func (s *Server) sceneItems(w http.ResponseWriter, r *http.Request, parentID string, filter stash.SceneFilter) {
	limit := boundedLimit(r, s.cfg.DefaultPageSize, s.cfg.MaxPageSize)
	start := startIndex(r)
	scenes, total, err := fetchWindow(start, limit, limit, func(page, perPage int) ([]stash.Scene, int, error) {
		result, err := s.stash.ListScenesBy(r.Context(), page, perPage, filter)
		return result.Scenes, result.Count, err
	})
	if err != nil {
		s.log.Error("list scenes failed", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}

	items := make([]map[string]any, 0, len(scenes))
	for _, scene := range scenes {
		items = append(items, s.formatItem(scene, parentID))
	}
	writeJSON(w, http.StatusOK, itemsResponse(items, start, total))
}

func (s *Server) performerItems(w http.ResponseWriter, r *http.Request) {
	limit := boundedLimit(r, s.cfg.DefaultPageSize, s.cfg.MaxPageSize)
	start := startIndex(r)
	sort, direction := entitySort(r)
	performers, total, err := fetchWindow(start, limit, limit, func(page, perPage int) ([]stash.Performer, int, error) {
		result, err := s.stash.ListPerformersBy(r.Context(), page, perPage, queryValue(r, "SearchTerm"), sort, direction)
		return result.Performers, result.Count, err
	})
	if err != nil {
		s.log.Error("list performers failed", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	items := make([]map[string]any, 0, len(performers))
	for _, performer := range performers {
		items = append(items, s.formatPerformerFolder(performer, "root-performers"))
	}
	writeJSON(w, http.StatusOK, itemsResponse(items, start, total))
}

func (s *Server) studioItems(w http.ResponseWriter, r *http.Request) {
	limit := boundedLimit(r, s.cfg.DefaultPageSize, s.cfg.MaxPageSize)
	start := startIndex(r)
	sort, direction := entitySort(r)
	studios, total, err := fetchWindow(start, limit, limit, func(page, perPage int) ([]stash.Studio, int, error) {
		result, err := s.stash.ListStudiosBy(r.Context(), page, perPage, queryValue(r, "SearchTerm"), sort, direction)
		return result.Studios, result.Count, err
	})
	if err != nil {
		s.log.Error("list studios failed", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	items := make([]map[string]any, 0, len(studios))
	for _, studio := range studios {
		items = append(items, s.formatStudio(studio, "root-studios"))
	}
	writeJSON(w, http.StatusOK, itemsResponse(items, start, total))
}

func (s *Server) tagItems(w http.ResponseWriter, r *http.Request) {
	limit := boundedLimit(r, s.cfg.DefaultPageSize, s.cfg.MaxPageSize)
	start := startIndex(r)
	sort, direction := entitySort(r)
	tags, total, err := fetchWindow(start, limit, limit, func(page, perPage int) ([]stash.Tag, int, error) {
		result, err := s.stash.ListTopTagsBy(r.Context(), page, perPage, queryValue(r, "SearchTerm"), sort, direction)
		return result.Tags, result.Count, err
	})
	if err != nil {
		s.log.Error("list tags failed", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	items := make([]map[string]any, 0, len(tags))
	for _, tag := range tags {
		items = append(items, s.formatTag(tag, "root-tags"))
	}
	writeJSON(w, http.StatusOK, itemsResponse(items, start, total))
}

func (s *Server) tagChildrenOrScenes(w http.ResponseWriter, r *http.Request, tagIDValue, parentID string) {
	tag, ok, err := s.stash.FindTag(r.Context(), tagIDValue)
	if err != nil {
		s.log.Error("find tag failed", "id", tagIDValue, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	if tag.ChildCount == 0 {
		filter := s.sceneFilterFromQuery(r)
		filter.TagID = tagIDValue
		filter.SearchTerm = queryValue(r, "SearchTerm")
		s.sceneItems(w, r, parentID, filter)
		return
	}

	limit := boundedLimit(r, s.cfg.DefaultPageSize, s.cfg.MaxPageSize)
	start := startIndex(r)
	sort, direction := entitySort(r)
	searchTerm := queryValue(r, "SearchTerm")
	includeAllScenes := searchTerm == ""
	childStart := start
	childLimit := limit
	if includeAllScenes {
		if start == 0 {
			childLimit--
		} else {
			childStart--
		}
	}
	children := []stash.Tag{}
	total := tag.ChildCount
	if childLimit > 0 {
		children, total, err = fetchWindow(childStart, childLimit, childLimit, func(page, perPage int) ([]stash.Tag, int, error) {
			result, err := s.stash.ListChildTagsBy(r.Context(), tagIDValue, page, perPage, searchTerm, sort, direction)
			return result.Tags, result.Count, err
		})
	}
	if err != nil {
		s.log.Error("list child tags failed", "id", tagIDValue, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	items := make([]map[string]any, 0, len(children)+1)
	if start == 0 && includeAllScenes {
		items = append(items, s.allScenesTagFolder(tag, parentID))
	}
	for _, child := range children {
		items = append(items, s.formatTag(child, parentID))
	}
	if includeAllScenes {
		total++
	}
	writeJSON(w, http.StatusOK, itemsResponse(items, start, total))
}

func (s *Server) latest(w http.ResponseWriter, r *http.Request) {
	parentID := queryValue(r, "ParentId")
	if parentID != "" && parentID != "root-scenes" {
		writeJSON(w, http.StatusOK, []map[string]any{})
		return
	}

	limit := boundedLimit(r, 16, s.cfg.MaxPageSize)
	scenes, err := s.stash.ListScenes(r.Context(), 1, limit)
	if err != nil {
		s.log.Error("latest scenes failed", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	items := make([]map[string]any, 0, len(scenes.Scenes))
	for _, scene := range scenes.Scenes {
		items = append(items, s.formatItem(scene, "root-scenes"))
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) itemByPath(w http.ResponseWriter, r *http.Request, p string) {
	parts := strings.Split(p, "/Items/")
	if len(parts) != 2 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	switch parts[1] {
	case "Latest":
		s.latest(w, r)
		return
	case "Resume":
		writeJSON(w, http.StatusOK, itemsResponse(nil, 0, 0))
		return
	}
	s.item(w, r, parts[1])
}

func (s *Server) item(w http.ResponseWriter, r *http.Request, itemID string) {
	if uuidKind(itemID) != "" {
		itemID = prefixedIDFromUUID(itemID)
	}
	switch {
	case itemID == "root-scenes":
		writeJSON(w, http.StatusOK, s.rootFolder("Scenes", "root-scenes"))
		return
	case itemID == "root-performers":
		writeJSON(w, http.StatusOK, s.rootFolder("Performers", "root-performers"))
		return
	case itemID == "root-studios":
		writeJSON(w, http.StatusOK, s.rootFolder("Studios", "root-studios"))
		return
	case itemID == "root-tags":
		writeJSON(w, http.StatusOK, s.rootFolder("Tags", "root-tags"))
		return
	case strings.HasPrefix(itemID, "person-"):
		s.person(w, r, numericEntityID(itemID, "person-"))
		return
	case strings.HasPrefix(itemID, "performer-"):
		s.performerFolder(w, r, numericEntityID(itemID, "performer-"))
		return
	case strings.HasPrefix(itemID, "studio-"):
		s.studio(w, r, numericEntityID(itemID, "studio-"))
		return
	case strings.HasPrefix(itemID, "tagall-"):
		id := numericEntityID(itemID, "tagall-")
		tag, ok, err := s.stash.FindTag(r.Context(), id)
		if err != nil {
			s.log.Error("find tag all-scenes folder failed", "id", id, "error", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
			return
		}
		writeJSON(w, http.StatusOK, s.allScenesTagFolder(tag, entityUUID("tag", tag.ID)))
		return
	case strings.HasPrefix(itemID, "tag-"):
		s.tag(w, r, numericEntityID(itemID, "tag-"))
		return
	}

	scene, ok, err := s.findScene(r.Context(), itemID)
	if err != nil {
		s.log.Error("find scene failed", "item_id", itemID, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	writeJSON(w, http.StatusOK, s.formatItem(scene, "root-scenes"))
}

func (s *Server) persons(w http.ResponseWriter, r *http.Request) {
	limit := boundedLimit(r, s.cfg.DefaultPageSize, s.cfg.MaxPageSize)
	start := startIndex(r)
	sort, direction := entitySort(r)
	performers, total, err := fetchWindow(start, limit, limit, func(page, perPage int) ([]stash.Performer, int, error) {
		result, err := s.stash.ListPerformersBy(r.Context(), page, perPage, queryValue(r, "SearchTerm"), sort, direction)
		return result.Performers, result.Count, err
	})
	if err != nil {
		s.log.Error("list persons failed", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	items := make([]map[string]any, 0, len(performers))
	for _, performer := range performers {
		items = append(items, s.formatPerformer(performer, ""))
	}
	writeJSON(w, http.StatusOK, itemsResponse(items, start, total))
}

func (s *Server) person(w http.ResponseWriter, r *http.Request, id string) {
	performer, ok, err := s.findPerformerRef(r.Context(), id)
	if err != nil {
		s.log.Error("find performer failed", "id", id, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	writeJSON(w, http.StatusOK, s.formatPerformer(performer, "root-performers"))
}

func (s *Server) performerFolder(w http.ResponseWriter, r *http.Request, id string) {
	performer, ok, err := s.stash.FindPerformer(r.Context(), id)
	if err != nil {
		s.log.Error("find performer folder failed", "id", id, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	writeJSON(w, http.StatusOK, s.formatPerformerFolder(performer, "root-performers"))
}

func (s *Server) genres(w http.ResponseWriter, r *http.Request) {
	s.tagItems(w, r)
}

func (s *Server) genre(w http.ResponseWriter, r *http.Request, name string) {
	tag, ok, err := s.stash.FindTagByName(r.Context(), name)
	if err != nil {
		s.log.Error("find genre failed", "name", name, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	writeJSON(w, http.StatusOK, s.formatTag(tag, "root-tags"))
}

func (s *Server) studio(w http.ResponseWriter, r *http.Request, id string) {
	studio, ok, err := s.stash.FindStudio(r.Context(), id)
	if err != nil {
		s.log.Error("find studio failed", "id", id, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	writeJSON(w, http.StatusOK, s.formatStudio(studio, "root-studios"))
}

func (s *Server) tag(w http.ResponseWriter, r *http.Request, id string) {
	tag, ok, err := s.stash.FindTag(r.Context(), id)
	if err != nil {
		s.log.Error("find tag failed", "id", id, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	writeJSON(w, http.StatusOK, s.formatTag(tag, "root-tags"))
}

func (s *Server) playbackInfo(w http.ResponseWriter, r *http.Request, itemID string) {
	scene, ok, err := s.findScene(r.Context(), itemID)
	if err != nil {
		s.log.Error("playback scene lookup failed", "item_id", itemID, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}

	source := s.mediaSource(scene)
	writeJSON(w, http.StatusOK, map[string]any{
		"MediaSources":  []map[string]any{source},
		"PlaySessionId": "stashfin-" + scene.ID,
	})
}

func (s *Server) redirectStream(w http.ResponseWriter, r *http.Request, itemID string) {
	scene, ok, err := s.findScene(r.Context(), itemID)
	if err != nil {
		s.log.Error("stream scene lookup failed", "item_id", itemID, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	streamURL := s.stash.PublicURL(stash.BestStream(scene))
	if streamURL == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "No stream available"})
		return
	}
	http.Redirect(w, r, streamURL, http.StatusFound)
}

func (s *Server) playing(w http.ResponseWriter, r *http.Request, p string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	var event struct {
		ItemID                 string `json:"ItemId"`
		PlaySessionID          string `json:"PlaySessionId"`
		PositionTicks          int64  `json:"PositionTicks"`
		PlaybackStartTimeTicks int64  `json:"PlaybackStartTimeTicks"`
	}
	if !decodeJSONBody(w, r, &event) {
		return
	}
	sceneIDValue := numericSceneID(event.ItemID)
	if sceneIDValue == "" || sceneIDValue == event.ItemID && !strings.HasPrefix(event.ItemID, "scene-") {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}

	resumeSeconds := ticksToSeconds(event.PositionTicks)
	stopped := strings.HasSuffix(p, "/stopped")
	sessionKey := event.ItemID
	if event.PlaySessionID != "" {
		sessionKey = event.PlaySessionID + "\x00" + event.ItemID
	}
	update := s.playback.observe(sessionKey, resumeSeconds, stopped)
	if update.duplicate {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if stopped || strings.HasSuffix(p, "/progress") {
		if err := s.stash.SaveSceneActivity(r.Context(), sceneIDValue, resumeSeconds, update.playDuration); err != nil {
			s.log.Warn("save scene activity failed", "scene_id", sceneIDValue, "error", err)
		}
	}
	if stopped {
		if scene, ok, err := s.stash.FindScene(r.Context(), sceneIDValue); err == nil && ok {
			duration := firstFile(scene).Duration
			if duration > 0 && resumeSeconds/duration >= 0.9 {
				if err := s.stash.AddScenePlay(r.Context(), sceneIDValue); err != nil {
					s.log.Warn("add scene play failed", "scene_id", sceneIDValue, "error", err)
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) userItemRating(w http.ResponseWriter, r *http.Request, itemID string) {
	sceneIDValue := numericSceneID(itemID)
	if sceneIDValue == "" || sceneIDValue == itemID && !strings.HasPrefix(itemID, "scene-") {
		writeJSON(w, http.StatusOK, userData(itemID, 0, 0, false))
		return
	}
	if r.Method == http.MethodDelete {
		if err := s.stash.UpdateSceneRating(r.Context(), sceneIDValue, 0); err != nil {
			s.log.Warn("clear scene rating failed", "scene_id", sceneIDValue, "error", err)
		}
		writeJSON(w, http.StatusOK, userData(itemID, 0, 0, false))
		return
	}
	if rating, ok := ratingValue(r); ok {
		if err := s.stash.UpdateSceneRating(r.Context(), sceneIDValue, rating); err != nil {
			s.log.Warn("update scene rating failed", "scene_id", sceneIDValue, "rating100", rating, "error", err)
		}
	}
	scene, ok, err := s.findScene(r.Context(), itemID)
	if err != nil || !ok {
		writeJSON(w, http.StatusOK, userData(itemID, 0, 0, false))
		return
	}
	writeJSON(w, http.StatusOK, userData(itemID, scene.ResumeTime, scene.PlayCount, scene.PlayCount > 0))
}

func (s *Server) userFavorite(w http.ResponseWriter, r *http.Request, itemID string) {
	scene, ok, err := s.findScene(r.Context(), itemID)
	if err != nil || !ok {
		writeJSON(w, http.StatusOK, userData(itemID, 0, 0, false))
		return
	}
	writeJSON(w, http.StatusOK, userData(itemID, scene.ResumeTime, scene.PlayCount, scene.PlayCount > 0))
}

func (s *Server) image(w http.ResponseWriter, r *http.Request, itemID string) {
	if strings.HasPrefix(itemID, "root-") {
		s.rootImage(w, r, itemID)
		return
	}
	if uuidKind(itemID) != "" {
		itemID = prefixedIDFromUUID(itemID)
	}
	if cached, ok := s.cachedImage(itemID); ok {
		s.proxyImage(w, r, cached)
		return
	}

	if strings.HasPrefix(itemID, "person-") {
		performer, ok, err := s.stash.FindPerformer(r.Context(), numericEntityID(itemID, "person-"))
		if err != nil {
			s.log.Error("image performer lookup failed", "item_id", itemID, "error", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
			return
		}
		if !ok || performer.ImagePath == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
			return
		}
		s.serveCachedImage(w, r, itemID, performer.ImagePath)
		return
	}
	if strings.HasPrefix(itemID, "performer-") {
		performer, ok, err := s.stash.FindPerformer(r.Context(), numericEntityID(itemID, "performer-"))
		if err != nil {
			s.log.Error("image performer folder lookup failed", "item_id", itemID, "error", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
			return
		}
		if !ok || performer.ImagePath == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
			return
		}
		s.serveCachedImage(w, r, itemID, performer.ImagePath)
		return
	}
	if strings.HasPrefix(itemID, "studio-") {
		studio, ok, err := s.stash.FindStudio(r.Context(), numericEntityID(itemID, "studio-"))
		if err != nil {
			s.log.Error("image studio lookup failed", "item_id", itemID, "error", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
			return
		}
		if !ok || studio.ImagePath == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
			return
		}
		s.serveCachedImage(w, r, itemID, studio.ImagePath)
		return
	}
	if strings.HasPrefix(itemID, "tag-") {
		tag, ok, err := s.stash.FindTag(r.Context(), numericEntityID(itemID, "tag-"))
		if err != nil {
			s.log.Error("image tag lookup failed", "item_id", itemID, "error", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
			return
		}
		if !ok || tag.ImagePath == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
			return
		}
		s.serveCachedImage(w, r, itemID, tag.ImagePath)
		return
	}

	scene, ok, err := s.findScene(r.Context(), itemID)
	if err != nil {
		s.log.Error("image scene lookup failed", "item_id", itemID, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	if !ok || scene.Paths.Screenshot == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	s.serveCachedImage(w, r, itemID, scene.Paths.Screenshot)
}

func (s *Server) rootImage(w http.ResponseWriter, r *http.Request, itemID string) {
	if queryValue(r, "Refresh") == "1" || strings.EqualFold(queryValue(r, "Refresh"), "true") {
		s.wallCache.Delete(itemID)
	}
	if cached, ok := s.cachedWall(itemID); ok {
		s.writeJPEG(w, cached)
		return
	}
	urls := s.rootImageURLs(r.Context(), itemID)
	if len(urls) == 0 && (itemID == "root-studios" || itemID == "root-tags") {
		urls = s.rootImageURLs(r.Context(), "root-scenes")
	}
	if len(urls) > 0 {
		if jpegBytes, err := s.libraryCover(r.Context(), urls, rootImageTitle(itemID)); err == nil && len(jpegBytes) > 0 {
			s.cacheWall(itemID, jpegBytes)
			s.writeJPEG(w, jpegBytes)
			return
		}
		s.proxyImage(w, r, urls[0])
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
}

func (s *Server) rootImageURLs(ctx context.Context, itemID string) []string {
	switch itemID {
	case "root-scenes":
		return s.latestSceneImageURLs(ctx, 12)
	case "root-performers":
		if urls := s.recentPerformerImageURLs(ctx, 12); len(urls) > 0 {
			return urls
		}
		page, err := s.stash.ListPerformers(ctx, 1, 60, "")
		if err != nil {
			return nil
		}
		urls := []string{}
		for _, performer := range page.Performers {
			if usableImage(performer.ImagePath) {
				urls = append(urls, s.stash.PublicURL(performer.ImagePath))
				if len(urls) >= 12 {
					break
				}
			}
		}
		return urls
	case "root-studios":
		if urls := s.recentStudioImageURLs(ctx, 12); len(urls) > 0 {
			return urls
		}
		page, err := s.stash.ListStudios(ctx, 1, 60, "")
		if err != nil {
			return nil
		}
		urls := []string{}
		for _, studio := range page.Studios {
			if usableImage(studio.ImagePath) {
				urls = append(urls, s.stash.PublicURL(studio.ImagePath))
				if len(urls) >= 12 {
					break
				}
			}
		}
		return urls
	case "root-tags":
		if urls := s.recentTagImageURLs(ctx, 12); len(urls) > 0 {
			return urls
		}
		page, err := s.stash.ListTopTagsBy(ctx, 1, 60, "", "name", "ASC")
		if err != nil {
			return nil
		}
		urls := []string{}
		for _, tag := range page.Tags {
			if usableImage(tag.ImagePath) {
				urls = append(urls, s.stash.PublicURL(tag.ImagePath))
				if len(urls) >= 12 {
					break
				}
			}
		}
		return urls
	}
	return nil
}

func (s *Server) latestSceneImageURLs(ctx context.Context, limit int) []string {
	scenes := s.latestCoverScenes(ctx, limit)
	urls := []string{}
	for _, scene := range scenes {
		if scene.Paths.Screenshot != "" {
			urls = appendPublicURL(urls, s.stash.PublicURL(scene.Paths.Screenshot), limit)
		}
	}
	return urls
}

func (s *Server) recentPerformerImageURLs(ctx context.Context, limit int) []string {
	scenes := s.latestCoverScenes(ctx, 40)
	urls := []string{}
	seen := map[string]bool{}
	for _, scene := range scenes {
		for _, performer := range scene.Performers {
			if seen[performer.ID] || !usableImage(performer.ImagePath) {
				continue
			}
			seen[performer.ID] = true
			urls = appendPublicURL(urls, s.stash.PublicURL(performer.ImagePath), limit)
			if len(urls) >= limit {
				return urls
			}
		}
	}
	return urls
}

func (s *Server) recentStudioImageURLs(ctx context.Context, limit int) []string {
	scenes := s.latestCoverScenes(ctx, 60)
	urls := []string{}
	seen := map[string]bool{}
	for _, scene := range scenes {
		if scene.Studio == nil || seen[scene.Studio.ID] || !usableImage(scene.Studio.ImagePath) {
			continue
		}
		seen[scene.Studio.ID] = true
		urls = appendPublicURL(urls, s.stash.PublicURL(scene.Studio.ImagePath), limit)
		if len(urls) >= limit {
			return urls
		}
	}
	return urls
}

func (s *Server) recentTagImageURLs(ctx context.Context, limit int) []string {
	scenes := s.latestCoverScenes(ctx, 60)
	urls := []string{}
	seen := map[string]bool{}
	for _, scene := range scenes {
		for _, tag := range scene.Tags {
			if seen[tag.ID] || !usableImage(tag.ImagePath) {
				continue
			}
			seen[tag.ID] = true
			urls = appendPublicURL(urls, s.stash.PublicURL(tag.ImagePath), limit)
			if len(urls) >= limit {
				return urls
			}
		}
	}
	return urls
}

func (s *Server) latestCoverScenes(ctx context.Context, limit int) []stash.Scene {
	page, err := s.stash.ListScenesBy(ctx, 1, limit, stash.SceneFilter{Sort: "created_at", Direction: "DESC"})
	if err != nil {
		return nil
	}
	return page.Scenes
}

func appendPublicURL(urls []string, publicURL string, limit int) []string {
	if publicURL == "" || len(urls) >= limit {
		return urls
	}
	return append(urls, publicURL)
}

func (s *Server) personImage(w http.ResponseWriter, r *http.Request, name string) {
	performer, ok, err := s.findPerformerRef(r.Context(), name)
	if err != nil {
		s.log.Error("person image lookup failed", "name", name, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	if !ok || performer.ImagePath == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	s.serveCachedImage(w, r, personID(performer.ID), performer.ImagePath)
}

func (s *Server) genreImage(w http.ResponseWriter, r *http.Request, name string) {
	tag, ok, err := s.stash.FindTagByName(r.Context(), name)
	if err != nil {
		s.log.Error("genre image lookup failed", "name", name, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Stash query failed"})
		return
	}
	if !ok || tag.ImagePath == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	s.serveCachedImage(w, r, tagID(tag.ID), tag.ImagePath)
}

func (s *Server) serveCachedImage(w http.ResponseWriter, r *http.Request, itemID, rawURL string) {
	publicURL := s.stash.PublicURL(rawURL)
	s.cacheImage(itemID, publicURL)
	s.proxyImage(w, r, publicURL)
}

func (s *Server) proxyImage(w http.ResponseWriter, r *http.Request, publicURL string) {
	resp, err := s.stash.GetURL(r.Context(), publicURL)
	if err != nil {
		s.log.Error("image fetch failed", "url", publicURL, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Image fetch failed"})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.log.Error("image fetch returned non-success", "url", publicURL, "status", resp.Status)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Image fetch failed"})
		return
	}
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if contentLength := resp.Header.Get("Content-Length"); contentLength != "" {
		w.Header().Set("Content-Length", contentLength)
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) libraryCover(ctx context.Context, urls []string, title string) ([]byte, error) {
	const (
		canvasW = 1280
		canvasH = 720
		cols    = 4
		rows    = 2
		gap     = 6
	)
	cellW := (canvasW - gap*(cols+1)) / cols
	cellH := (canvasH - gap*(rows+1)) / rows
	canvas := image.NewRGBA(image.Rect(0, 0, canvasW, canvasH))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.RGBA{12, 12, 13, 255}}, image.Point{}, draw.Src)

	drawn := 0
	for _, rawURL := range urls {
		if drawn >= cols*rows {
			break
		}
		img, err := s.fetchImage(ctx, rawURL)
		if err != nil {
			continue
		}
		x := gap + (drawn%cols)*(cellW+gap)
		y := gap + (drawn/cols)*(cellH+gap)
		drawCropped(canvas, image.Rect(x, y, x+cellW, y+cellH), img)
		drawn++
	}
	if drawn == 0 {
		return nil, io.ErrUnexpectedEOF
	}

	const (
		textX = 98
		barX  = 58
		barW  = 16
	)
	fillRectAlpha(canvas, canvas.Bounds(), color.RGBA{5, 5, 6, 88})
	fillHorizontalGradient(canvas, canvas.Bounds(), color.RGBA{7, 7, 8, 150}, color.RGBA{7, 7, 8, 20})
	fillRectAlpha(canvas, image.Rect(0, canvasH-300, canvasW, canvasH), color.RGBA{0, 0, 0, 82})
	fillRectAlpha(canvas, image.Rect(barX, canvasH-290, barX+barW, canvasH-112), color.RGBA{255, 132, 0, 238})
	drawBlockText(canvas, strings.ToUpper(title), textX, canvasH-266, 11, color.RGBA{250, 250, 244, 252})

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Server) fetchImage(ctx context.Context, publicURL string) (image.Image, error) {
	resp, err := s.stash.GetURL(ctx, publicURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, io.ErrUnexpectedEOF
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCoverImageBytes {
		return nil, fmt.Errorf("image exceeds %d bytes", maxCoverImageBytes)
	}
	imageConfig, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if !coverImageDimensionsAllowed(imageConfig) {
		return nil, fmt.Errorf("image dimensions exceed safety limits: %dx%d", imageConfig.Width, imageConfig.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

func coverImageDimensionsAllowed(cfg image.Config) bool {
	return cfg.Width > 0 && cfg.Height > 0 &&
		cfg.Width <= maxCoverImageSide && cfg.Height <= maxCoverImageSide &&
		int64(cfg.Width)*int64(cfg.Height) <= maxCoverImagePixels
}

func drawCropped(dst draw.Image, rect image.Rectangle, src image.Image) {
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	dstW := rect.Dx()
	dstH := rect.Dy()
	if srcW <= 0 || srcH <= 0 || dstW <= 0 || dstH <= 0 {
		return
	}
	srcAspect := float64(srcW) / float64(srcH)
	dstAspect := float64(dstW) / float64(dstH)
	crop := srcBounds
	if srcAspect > dstAspect {
		newW := int(float64(srcH) * dstAspect)
		x0 := srcBounds.Min.X + (srcW-newW)/2
		crop = image.Rect(x0, srcBounds.Min.Y, x0+newW, srcBounds.Max.Y)
	} else {
		newH := int(float64(srcW) / dstAspect)
		y0 := srcBounds.Min.Y + (srcH-newH)/2
		crop = image.Rect(srcBounds.Min.X, y0, srcBounds.Max.X, y0+newH)
	}
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			sx := crop.Min.X + (x-rect.Min.X)*crop.Dx()/dstW
			sy := crop.Min.Y + (y-rect.Min.Y)*crop.Dy()/dstH
			dst.Set(x, y, src.At(sx, sy))
		}
	}
}

func fillRectAlpha(dst *image.RGBA, rect image.Rectangle, c color.RGBA) {
	rect = rect.Intersect(dst.Bounds())
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			blendPixel(dst, x, y, c)
		}
	}
}

func fillHorizontalGradient(dst *image.RGBA, rect image.Rectangle, left, right color.RGBA) {
	rect = rect.Intersect(dst.Bounds())
	width := rect.Dx()
	if width <= 1 {
		fillRectAlpha(dst, rect, left)
		return
	}
	for x := rect.Min.X; x < rect.Max.X; x++ {
		t := float64(x-rect.Min.X) / float64(width-1)
		c := color.RGBA{
			R: uint8(float64(left.R)*(1-t) + float64(right.R)*t),
			G: uint8(float64(left.G)*(1-t) + float64(right.G)*t),
			B: uint8(float64(left.B)*(1-t) + float64(right.B)*t),
			A: uint8(float64(left.A)*(1-t) + float64(right.A)*t),
		}
		for y := rect.Min.Y; y < rect.Max.Y; y++ {
			blendPixel(dst, x, y, c)
		}
	}
}

func blendPixel(dst *image.RGBA, x, y int, c color.RGBA) {
	if c.A == 0 {
		return
	}
	i := dst.PixOffset(x, y)
	a := int(c.A)
	inv := 255 - a
	dst.Pix[i+0] = uint8((int(c.R)*a + int(dst.Pix[i+0])*inv) / 255)
	dst.Pix[i+1] = uint8((int(c.G)*a + int(dst.Pix[i+1])*inv) / 255)
	dst.Pix[i+2] = uint8((int(c.B)*a + int(dst.Pix[i+2])*inv) / 255)
	dst.Pix[i+3] = 255
}

func drawBlockText(dst *image.RGBA, text string, x, y, scale int, c color.RGBA) {
	cursor := x
	for _, r := range text {
		if r == ' ' {
			cursor += 4 * scale
			continue
		}
		glyph, ok := blockGlyphs[r]
		if !ok {
			cursor += 6 * scale
			continue
		}
		for gy, row := range glyph {
			for gx, px := range row {
				if px != '1' {
					continue
				}
				fillRectAlpha(dst, image.Rect(cursor+gx*scale, y+gy*scale, cursor+(gx+1)*scale, y+(gy+1)*scale), c)
			}
		}
		cursor += 6 * scale
	}
}

var blockGlyphs = map[rune][7]string{
	'A': {"01110", "10001", "10001", "11111", "10001", "10001", "10001"},
	'C': {"01111", "10000", "10000", "10000", "10000", "10000", "01111"},
	'D': {"11110", "10001", "10001", "10001", "10001", "10001", "11110"},
	'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
	'F': {"11111", "10000", "10000", "11110", "10000", "10000", "10000"},
	'G': {"01111", "10000", "10000", "10111", "10001", "10001", "01111"},
	'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"},
	'I': {"11111", "00100", "00100", "00100", "00100", "00100", "11111"},
	'M': {"10001", "11011", "10101", "10101", "10001", "10001", "10001"},
	'N': {"10001", "11001", "10101", "10011", "10001", "10001", "10001"},
	'O': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
	'P': {"11110", "10001", "10001", "11110", "10000", "10000", "10000"},
	'R': {"11110", "10001", "10001", "11110", "10100", "10010", "10001"},
	'S': {"01111", "10000", "10000", "01110", "00001", "00001", "11110"},
	'T': {"11111", "00100", "00100", "00100", "00100", "00100", "00100"},
	'U': {"10001", "10001", "10001", "10001", "10001", "10001", "01110"},
}

func (s *Server) writeJPEG(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) cacheImage(itemID, publicURL string) {
	if itemID == "" || publicURL == "" {
		return
	}
	s.imageCache.Store(itemID, publicURL)
}

func (s *Server) cacheWall(itemID string, data []byte) {
	if itemID == "" || len(data) == 0 {
		return
	}
	s.wallCache.Store(itemID, wallCacheEntry{data: data, cachedAt: time.Now()})
}

func (s *Server) cachedWall(itemID string) ([]byte, bool) {
	value, ok := s.wallCache.Load(itemID)
	if !ok {
		return nil, false
	}
	entry, ok := value.(wallCacheEntry)
	if !ok || len(entry.data) == 0 {
		return nil, false
	}
	if time.Since(entry.cachedAt) > 6*time.Hour {
		s.wallCache.Delete(itemID)
		return nil, false
	}
	return entry.data, true
}

func (s *Server) cachedImage(itemID string) (string, bool) {
	value, ok := s.imageCache.Load(itemID)
	if !ok {
		return "", false
	}
	publicURL, ok := value.(string)
	return publicURL, ok && publicURL != ""
}

func (s *Server) formatItem(scene stash.Scene, parentID string) map[string]any {
	title := sceneTitle(scene)
	file := firstFile(scene)
	itemID := sceneID(scene.ID)
	if scene.Paths.Screenshot != "" {
		s.cacheImage(itemID, s.stash.PublicURL(scene.Paths.Screenshot))
	}

	item := map[string]any{
		"Name":         title,
		"SortName":     title,
		"Id":           itemID,
		"ServerId":     s.cfg.ServerID,
		"Type":         "Movie",
		"IsFolder":     false,
		"MediaType":    "Video",
		"ParentId":     parentID,
		"ImageTags":    map[string]string{"Primary": "stash"},
		"RunTimeTicks": runTimeTicks(file.Duration),
		"UserData": map[string]any{
			"PlaybackPositionTicks": runTimeTicks(scene.ResumeTime),
			"PlayCount":             scene.PlayCount,
			"IsFavorite":            false,
			"Played":                scene.PlayCount > 0,
			"Key":                   itemID,
		},
		"MediaSources": []map[string]any{s.mediaSource(scene)},
	}

	if scene.Date != "" && len(scene.Date) >= 4 {
		if year, err := strconv.Atoi(scene.Date[:4]); err == nil {
			item["ProductionYear"] = year
			item["PremiereDate"] = scene.Date + "T00:00:00.0000000Z"
		}
	}
	if scene.CreatedAt != "" {
		item["DateCreated"] = scene.CreatedAt
	}
	if scene.Details != "" {
		item["Overview"] = scene.Details
	}
	if scene.Director != "" {
		item["Director"] = scene.Director
	}
	if scene.Rating100 > 0 {
		item["CommunityRating"] = float64(scene.Rating100) / 10
		item["CriticRating"] = scene.Rating100
	}
	if len(scene.URLs) > 0 {
		item["ExternalUrls"] = externalURLs(scene.URLs)
	}
	if len(scene.StashIDs) > 0 {
		item["ProviderIds"] = providerIDs(scene.StashIDs)
	}
	if scene.Studio != nil && scene.Studio.Name != "" {
		item["Studio"] = scene.Studio.Name
		item["Studios"] = []map[string]any{{
			"Name": scene.Studio.Name,
			"Id":   entityUUID("studio", scene.Studio.ID),
		}}
	}
	if len(scene.Tags) > 0 {
		tags := make([]string, 0, len(scene.Tags))
		for _, tag := range scene.Tags {
			if tag.Name != "" {
				tags = append(tags, tag.Name)
			}
		}
		item["Tags"] = tags
		item["Genres"] = firstN(tags, 5)
	}
	if len(scene.Performers) > 0 {
		people := make([]map[string]any, 0, len(scene.Performers))
		for _, performer := range scene.Performers {
			if performer.Name == "" {
				continue
			}
			if performer.ImagePath != "" {
				s.cacheImage(personID(performer.ID), s.stash.PublicURL(performer.ImagePath))
			}
			people = append(people, map[string]any{
				"Name":            performer.Name,
				"Type":            "Actor",
				"Role":            "",
				"Id":              entityUUID("person", performer.ID),
				"PrimaryImageTag": imageTag(performer.ImagePath),
			})
		}
		item["People"] = people
	}
	return item
}

func (s *Server) formatPerformer(performer stash.Performer, parentID string) map[string]any {
	itemID := entityUUID("person", performer.ID)
	if performer.ImagePath != "" {
		s.cacheImage(itemID, s.stash.PublicURL(performer.ImagePath))
		s.cacheImage(personID(performer.ID), s.stash.PublicURL(performer.ImagePath))
	}
	item := s.folderItem(performer.Name, itemID, parentID, "Person", performer.SceneCount)
	item["IsFolder"] = false
	delete(item, "CollectionType")
	delete(item, "ChildCount")
	delete(item, "RecursiveItemCount")
	item["PrimaryImageTag"] = imageTag(performer.ImagePath)
	item["ImageTags"] = imageTags(performer.ImagePath)
	item["Overview"] = performer.Details
	item["PremiereDate"] = ""
	item["ExternalUrls"] = externalURLs(performer.URLs)
	item["UserData"] = map[string]any{"IsFavorite": performer.Favorite, "Key": itemID}
	if performer.Disambiguation != "" {
		item["OriginalTitle"] = performer.Disambiguation
	}
	if performer.Rating100 > 0 {
		item["CommunityRating"] = float64(performer.Rating100) / 10
	}
	return item
}

func (s *Server) formatPerformerFolder(performer stash.Performer, parentID string) map[string]any {
	itemID := entityUUID("performer", performer.ID)
	if performer.ImagePath != "" {
		s.cacheImage(itemID, s.stash.PublicURL(performer.ImagePath))
		s.cacheImage(performerID(performer.ID), s.stash.PublicURL(performer.ImagePath))
	}
	item := s.folderItem(performer.Name, itemID, parentID, "Folder", performer.SceneCount)
	item["PrimaryImageTag"] = imageTag(performer.ImagePath)
	item["ImageTags"] = imageTags(performer.ImagePath)
	item["Overview"] = performer.Details
	item["UserData"] = map[string]any{"IsFavorite": performer.Favorite, "Key": itemID}
	if performer.Disambiguation != "" {
		item["OriginalTitle"] = performer.Disambiguation
	}
	if performer.Rating100 > 0 {
		item["CommunityRating"] = float64(performer.Rating100) / 10
	}
	return item
}

func (s *Server) formatStudio(studio stash.Studio, parentID string) map[string]any {
	itemID := entityUUID("studio", studio.ID)
	if studio.ImagePath != "" {
		s.cacheImage(itemID, s.stash.PublicURL(studio.ImagePath))
		s.cacheImage(studioID(studio.ID), s.stash.PublicURL(studio.ImagePath))
	}
	item := s.folderItem(studio.Name, itemID, parentID, "Folder", studio.SceneCount)
	item["OriginalType"] = "Studio"
	item["PrimaryImageTag"] = imageTag(studio.ImagePath)
	item["ImageTags"] = imageTags(studio.ImagePath)
	item["Overview"] = studio.Details
	item["ExternalUrls"] = externalURLs(studio.URLs)
	item["UserData"] = map[string]any{"IsFavorite": studio.Favorite, "Key": itemID}
	if studio.Rating100 > 0 {
		item["CommunityRating"] = float64(studio.Rating100) / 10
	}
	return item
}

func (s *Server) formatTag(tag stash.Tag, parentID string) map[string]any {
	itemID := entityUUID("tag", tag.ID)
	if tag.ImagePath != "" {
		s.cacheImage(itemID, s.stash.PublicURL(tag.ImagePath))
		s.cacheImage(tagID(tag.ID), s.stash.PublicURL(tag.ImagePath))
	}
	name := firstNonEmpty(tag.Name, tag.SortName)
	childCount := tag.SceneCount
	if tag.ChildCount > 0 {
		childCount = tag.ChildCount + 1
	}
	item := s.folderItem(name, itemID, parentID, "Folder", childCount)
	item["OriginalType"] = "Genre"
	item["SortName"] = firstNonEmpty(tag.SortName, name)
	item["PrimaryImageTag"] = imageTag(tag.ImagePath)
	item["ImageTags"] = imageTags(tag.ImagePath)
	item["Overview"] = tag.Description
	item["RecursiveItemCount"] = tag.SceneCount
	item["ChildCount"] = childCount
	item["UserData"] = map[string]any{"IsFavorite": tag.Favorite, "Key": itemID}
	if len(tag.Aliases) > 0 {
		item["Tags"] = tag.Aliases
	}
	return item
}

func (s *Server) allScenesTagFolder(tag stash.Tag, parentID string) map[string]any {
	name := firstNonEmpty(tag.Name, tag.SortName)
	itemID := "tagall-" + tag.ID
	item := s.folderItem("All Scenes", itemID, parentID, "Folder", tag.SceneCount)
	item["OriginalType"] = "Genre"
	item["SortName"] = "000 All Scenes"
	item["Overview"] = "All scenes tagged " + name + ", including child tags."
	item["UserData"] = map[string]any{"IsFavorite": false, "Key": itemID}
	if tag.ImagePath != "" {
		s.cacheImage(itemID, s.stash.PublicURL(tag.ImagePath))
		item["PrimaryImageTag"] = imageTag(tag.ImagePath)
		item["ImageTags"] = imageTags(tag.ImagePath)
	}
	return item
}

func (s *Server) folderItem(name, id, parentID, itemType string, childCount int) map[string]any {
	return map[string]any{
		"Name":               name,
		"SortName":           name,
		"Id":                 id,
		"ServerId":           s.cfg.ServerID,
		"Type":               itemType,
		"IsFolder":           true,
		"MediaType":          "",
		"ParentId":           parentID,
		"CollectionType":     "movies",
		"RecursiveItemCount": childCount,
		"ChildCount":         childCount,
	}
}

func (s *Server) mediaSource(scene stash.Scene) map[string]any {
	file := firstFile(scene)
	itemID := sceneID(scene.ID)
	publicStreamURL := s.stash.PublicURL(stash.BestStream(scene))
	container := strings.ToLower(firstNonEmpty(file.Format, "mp4"))

	return map[string]any{
		"Id":                   itemID,
		"Path":                 publicStreamURL,
		"Protocol":             "Http",
		"Type":                 "Default",
		"Container":            container,
		"Size":                 file.Size,
		"RunTimeTicks":         runTimeTicks(file.Duration),
		"SupportsDirectPlay":   true,
		"SupportsDirectStream": true,
		"SupportsTranscoding":  false,
		"IsRemote":             true,
		"MediaStreams": []map[string]any{
			{
				"Index":                  0,
				"Type":                   "Video",
				"Codec":                  strings.ToLower(file.VideoCodec),
				"Width":                  file.Width,
				"Height":                 file.Height,
				"AverageFrameRate":       file.FrameRate,
				"BitRate":                file.BitRate,
				"IsDefault":              true,
				"IsForced":               false,
				"IsExternal":             false,
				"SupportsExternalStream": false,
			},
			{
				"Index":      1,
				"Type":       "Audio",
				"Codec":      strings.ToLower(file.AudioCodec),
				"IsDefault":  true,
				"IsForced":   false,
				"IsExternal": false,
			},
		},
	}
}

func (s *Server) findScene(ctx context.Context, itemID string) (stash.Scene, bool, error) {
	return s.stash.FindScene(ctx, numericSceneID(itemID))
}

func (s *Server) findPerformerRef(ctx context.Context, ref string) (stash.Performer, bool, error) {
	if uuidKind(ref) == "person" {
		return s.stash.FindPerformer(ctx, numericEntityRef(ref, "person"))
	}
	if strings.HasPrefix(ref, "person-") {
		return s.stash.FindPerformer(ctx, numericEntityID(ref, "person-"))
	}
	if _, err := strconv.Atoi(ref); err == nil {
		return s.stash.FindPerformer(ctx, ref)
	}
	return s.stash.FindPerformerByName(ctx, ref)
}

func (s *Server) validToken(r *http.Request) bool {
	token := r.Header.Get("X-Emby-Token")
	if token == "" {
		token = r.Header.Get("X-MediaBrowser-Token")
	}
	if token == "" {
		auth := r.Header.Get("Authorization")
		token = bearerToken(auth)
		if token == "" {
			token = mediaBrowserToken(auth)
		}
	}
	if token == "" {
		token = mediaBrowserToken(r.Header.Get("X-Emby-Authorization"))
	}
	if token == "" {
		token = r.URL.Query().Get("api_key")
	}
	return token != "" && token == s.cfg.AccessToken
}

func isPublic(p string) bool {
	switch p {
	case "/system/info", "/system/info/public", "/system/ping", "/branding/configuration", "/users", "/users/authenticatebyname", "/healthz":
		return true
	default:
		return false
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func itemsResponse(items []map[string]any, start, total int) map[string]any {
	if items == nil {
		items = []map[string]any{}
	}
	return map[string]any{
		"Items":            items,
		"TotalRecordCount": total,
		"StartIndex":       start,
	}
}

func userData(itemID string, resumeTime float64, playCount int, played bool) map[string]any {
	return map[string]any{
		"PlaybackPositionTicks": runTimeTicks(resumeTime),
		"PlayCount":             playCount,
		"IsFavorite":            false,
		"Played":                played,
		"Key":                   itemID,
	}
}

func ratingValue(r *http.Request) (int, bool) {
	raw := firstNonEmpty(queryValue(r, "rating"), queryValue(r, "Rating"), queryValue(r, "value"), queryValue(r, "Value"))
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	if value <= 10 {
		value *= 10
	}
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return int(value + 0.5), true
}

func startIndex(r *http.Request) int {
	value, _ := strconv.Atoi(queryValue(r, "StartIndex"))
	if value < 0 {
		return 0
	}
	return value
}

func boundedLimit(r *http.Request, fallback, max int) int {
	value, _ := strconv.Atoi(queryValue(r, "Limit"))
	if value < 1 {
		value = fallback
	}
	if value > max {
		value = max
	}
	return value
}

func itemIDFromPlaybackPath(p string) string {
	return pathSegment(p, 1)
}

func itemIDFromImagePath(p string) string {
	return pathSegment(p, 1)
}

func itemIDFromVideoPath(p string) string {
	return pathSegment(p, 1)
}

func pathSegment(p string, index int) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if index < 0 || index >= len(parts) {
		return ""
	}
	value, err := url.PathUnescape(parts[index])
	if err != nil {
		return parts[index]
	}
	return value
}

func queryValue(r *http.Request, key string) string {
	for name, values := range r.URL.Query() {
		if strings.EqualFold(name, key) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func (s *Server) sceneFilterFromQuery(r *http.Request) stash.SceneFilter {
	sort, direction := sceneSort(r)
	filter := stash.SceneFilter{
		SearchTerm: queryValue(r, "SearchTerm"),
		Sort:       sort,
		Direction:  direction,
	}
	if value := firstCSV(queryValue(r, "PersonIds")); value != "" {
		filter.PerformerID = numericEntityRef(value, "person")
	}
	if value := firstCSV(queryValue(r, "StudioIds")); value != "" {
		filter.StudioID = numericEntityRef(value, "studio")
	}
	if value := firstCSV(queryValue(r, "GenreIds")); value != "" {
		filter.TagID = numericEntityRef(value, "tag")
	}
	if filter.TagID == "" {
		if value := firstCSV(firstNonEmpty(queryValue(r, "Tags"), queryValue(r, "Genres"))); value != "" {
			if tagID, ok := s.tagIDByName(r.Context(), value); ok {
				filter.TagID = tagID
			}
		}
	}
	return filter
}

func numericSceneID(itemID string) string {
	return strings.TrimPrefix(itemID, "scene-")
}

func numericEntityID(itemID, prefix string) string {
	return strings.TrimPrefix(itemID, prefix)
}

func sceneID(id string) string {
	return "scene-" + id
}

func personID(id string) string {
	return "person-" + id
}

func performerID(id string) string {
	return "performer-" + id
}

func studioID(id string) string {
	return "studio-" + id
}

func tagID(id string) string {
	return "tag-" + id
}

func entityUUID(kind, id string) string {
	prefix := "00000000"
	switch kind {
	case "person":
		prefix = "10000000"
	case "performer":
		prefix = "11000000"
	case "studio":
		prefix = "20000000"
	case "tag":
		prefix = "30000000"
	}
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || n >= 1_000_000_000_000 {
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(kind + "\x00" + id))
		n = hash.Sum64()%1_000_000_000_000 + 1
	}
	return prefix + "-0000-0000-0000-" + leftPad(strconv.FormatUint(n, 10), 12, "0")
}

func uuidKind(value string) string {
	switch {
	case strings.HasPrefix(value, "10000000-0000-0000-0000-"):
		return "person"
	case strings.HasPrefix(value, "11000000-0000-0000-0000-"):
		return "performer"
	case strings.HasPrefix(value, "20000000-0000-0000-0000-"):
		return "studio"
	case strings.HasPrefix(value, "30000000-0000-0000-0000-"):
		return "tag"
	default:
		return ""
	}
}

func prefixedIDFromUUID(value string) string {
	kind := uuidKind(value)
	if kind == "" {
		return value
	}
	id := numericEntityRef(value, kind)
	switch kind {
	case "person":
		return personID(id)
	case "performer":
		return performerID(id)
	case "studio":
		return studioID(id)
	case "tag":
		return tagID(id)
	default:
		return value
	}
}

func numericEntityRef(value, kind string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, kind+"-") {
		return strings.TrimPrefix(value, kind+"-")
	}
	parts := strings.Split(value, "-")
	if len(parts) == 5 {
		return strings.TrimLeft(parts[4], "0")
	}
	return value
}

func firstCSV(value string) string {
	parts := strings.Split(value, ",")
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func rootImageTitle(itemID string) string {
	switch itemID {
	case "root-scenes":
		return "Scenes"
	case "root-performers":
		return "Performers"
	case "root-studios":
		return "Studios"
	case "root-tags":
		return "Tags"
	default:
		return "Stashfin"
	}
}

func sceneSort(r *http.Request) (string, string) {
	sortBy := strings.ToLower(firstCSV(queryValue(r, "SortBy")))
	sort := "created_at"
	switch sortBy {
	case "sortname", "name":
		sort = "title"
	case "productionyear", "premieredate", "date":
		sort = "date"
	case "datecreated":
		sort = "created_at"
	case "dateplayed":
		sort = "last_played_at"
	case "playcount":
		sort = "play_count"
	case "runtime":
		sort = "duration"
	case "criticrating", "communityrating", "rating":
		sort = "rating"
	case "random":
		sort = "random"
	}
	return sort, sortDirection(r, "DESC")
}

func entitySort(r *http.Request) (string, string) {
	sortBy := strings.ToLower(firstCSV(queryValue(r, "SortBy")))
	sort := "name"
	switch sortBy {
	case "datecreated":
		sort = "created_at"
	case "random":
		sort = "random"
	}
	return sort, sortDirection(r, "ASC")
}

func sortDirection(r *http.Request, fallback string) string {
	switch strings.ToLower(firstCSV(queryValue(r, "SortOrder"))) {
	case "descending", "desc":
		return "DESC"
	case "ascending", "asc":
		return "ASC"
	default:
		return fallback
	}
}

func (s *Server) tagIDByName(ctx context.Context, name string) (string, bool) {
	tag, ok, err := s.stash.FindTagByName(ctx, name)
	if err != nil || !ok {
		return "", false
	}
	return tag.ID, true
}

func leftPad(value string, width int, pad string) string {
	for len(value) < width {
		value = pad + value
	}
	return value
}

func sceneTitle(scene stash.Scene) string {
	if scene.Title != "" {
		return scene.Title
	}
	if scene.Code != "" {
		return scene.Code
	}
	if len(scene.Files) > 0 && scene.Files[0].Path != "" {
		base := path.Base(scene.Files[0].Path)
		return strings.TrimSuffix(base, path.Ext(base))
	}
	return "Scene " + scene.ID
}

func firstFile(scene stash.Scene) stash.VideoFile {
	if len(scene.Files) == 0 {
		return stash.VideoFile{}
	}
	return scene.Files[0]
}

func runTimeTicks(seconds float64) int64 {
	return int64(seconds * 10_000_000)
}

func ticksToSeconds(ticks int64) float64 {
	if ticks <= 0 {
		return 0
	}
	return float64(ticks) / 10_000_000
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstN(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[:n]
}

func imageTag(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	return "stash-" + hashImageTag(rawURL)
}

func imageTags(rawURL string) map[string]string {
	if rawURL == "" {
		return map[string]string{}
	}
	return map[string]string{"Primary": imageTag(rawURL)}
}

func (s *Server) rootImageTag(itemID string) string {
	return "wall-" + hashImageTag(itemID+"-"+s.imageSeed)
}

func hashImageTag(value string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return strconv.FormatUint(uint64(h.Sum32()), 36)
}

func usableImage(rawURL string) bool {
	return rawURL != "" && !strings.Contains(rawURL, "default=true")
}

func externalURLs(urls []string) []map[string]string {
	out := make([]map[string]string, 0, len(urls))
	for i, rawURL := range urls {
		if strings.TrimSpace(rawURL) == "" {
			continue
		}
		out = append(out, map[string]string{
			"Name": "URL " + strconv.Itoa(i+1),
			"Url":  rawURL,
		})
	}
	return out
}

func providerIDs(ids []stash.StashID) map[string]string {
	out := map[string]string{}
	for _, id := range ids {
		if id.Endpoint == "" || id.StashID == "" {
			continue
		}
		out["stash:"+id.Endpoint] = id.StashID
	}
	return out
}

func bearerToken(value string) string {
	if strings.HasPrefix(value, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
	}
	return ""
}

func mediaBrowserToken(value string) string {
	const marker = `Token="`
	start := strings.Index(value, marker)
	if start == -1 {
		return ""
	}
	rest := value[start+len(marker):]
	end := strings.Index(rest, `"`)
	if end == -1 {
		return ""
	}
	return rest[:end]
}

func LogRequest(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		logger.Info("request", "method", r.Method, "path", r.URL.Path, "query", r.URL.RawQuery, "status", recorder.status, "duration", time.Since(start), "user_agent", r.UserAgent())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
