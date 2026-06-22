package stash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	endpoint  string
	apiKey    string
	publicURL string
	http      *http.Client
}

func NewClient(internalURL, publicURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		endpoint:  strings.TrimRight(internalURL, "/") + "/graphql",
		apiKey:    apiKey,
		publicURL: strings.TrimRight(publicURL, "/"),
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) Check(ctx context.Context) (string, error) {
	var out struct {
		Version struct {
			Version string `json:"version"`
		} `json:"version"`
	}
	if err := c.query(ctx, `query { version { version } }`, nil, &out); err != nil {
		return "", err
	}
	return out.Version.Version, nil
}

func (c *Client) ListScenes(ctx context.Context, page, perPage int) (ScenePage, error) {
	return c.ListScenesBy(ctx, page, perPage, SceneFilter{})
}

type SceneFilter struct {
	PerformerID string
	StudioID    string
	TagID       string
	SearchTerm  string
	Sort        string
	Direction   string
}

func (c *Client) ListScenesBy(ctx context.Context, page, perPage int, filter SceneFilter) (ScenePage, error) {
	var out struct {
		FindScenes struct {
			Count  int     `json:"count"`
			Scenes []Scene `json:"scenes"`
		} `json:"findScenes"`
	}

	vars := map[string]any{
		"find": map[string]any{
			"page":      page,
			"per_page":  perPage,
			"sort":      firstNonEmpty(filter.Sort, "created_at"),
			"direction": firstNonEmpty(filter.Direction, "DESC"),
		},
		"scene": sceneFilterVars(filter),
	}
	if filter.SearchTerm != "" {
		vars["find"].(map[string]any)["q"] = filter.SearchTerm
	}
	if err := c.query(ctx, listScenesQuery, vars, &out); err != nil {
		if isInvalidSort(err) && filter.Sort != "created_at" {
			vars["find"].(map[string]any)["sort"] = "created_at"
			vars["find"].(map[string]any)["direction"] = "DESC"
			if retryErr := c.query(ctx, listScenesQuery, vars, &out); retryErr != nil {
				return ScenePage{}, retryErr
			}
		} else {
			return ScenePage{}, err
		}
	}

	return ScenePage{
		Scenes: out.FindScenes.Scenes,
		Count:  out.FindScenes.Count,
	}, nil
}

func (c *Client) ListPerformers(ctx context.Context, page, perPage int, searchTerm string) (PerformerPage, error) {
	return c.ListPerformersBy(ctx, page, perPage, searchTerm, "name", "ASC")
}

func (c *Client) ListPerformersBy(ctx context.Context, page, perPage int, searchTerm, sort, direction string) (PerformerPage, error) {
	var out struct {
		FindPerformers struct {
			Count      int         `json:"count"`
			Performers []Performer `json:"performers"`
		} `json:"findPerformers"`
	}
	vars := findVars(page, perPage, firstNonEmpty(sort, "name"), firstNonEmpty(direction, "ASC"), searchTerm)
	if err := c.query(ctx, listPerformersQuery, vars, &out); err != nil {
		if isInvalidSort(err) && sort != "name" {
			vars["find"].(map[string]any)["sort"] = "name"
			vars["find"].(map[string]any)["direction"] = "ASC"
			if retryErr := c.query(ctx, listPerformersQuery, vars, &out); retryErr != nil {
				return PerformerPage{}, retryErr
			}
		} else {
			return PerformerPage{}, err
		}
	}
	return PerformerPage{Performers: out.FindPerformers.Performers, Count: out.FindPerformers.Count}, nil
}

func (c *Client) FindPerformer(ctx context.Context, id string) (Performer, bool, error) {
	var out struct {
		FindPerformer *Performer `json:"findPerformer"`
	}
	if err := c.query(ctx, findPerformerQuery, map[string]any{"id": id}, &out); err != nil {
		return Performer{}, false, err
	}
	if out.FindPerformer == nil {
		return Performer{}, false, nil
	}
	return *out.FindPerformer, true, nil
}

func (c *Client) FindPerformerByName(ctx context.Context, name string) (Performer, bool, error) {
	page, err := c.ListPerformers(ctx, 1, 10, name)
	if err != nil {
		return Performer{}, false, err
	}
	for _, performer := range page.Performers {
		if strings.EqualFold(performer.Name, name) {
			return performer, true, nil
		}
	}
	if len(page.Performers) == 0 {
		return Performer{}, false, nil
	}
	return page.Performers[0], true, nil
}

func (c *Client) ListStudios(ctx context.Context, page, perPage int, searchTerm string) (StudioPage, error) {
	return c.ListStudiosBy(ctx, page, perPage, searchTerm, "name", "ASC")
}

func (c *Client) ListStudiosBy(ctx context.Context, page, perPage int, searchTerm, sort, direction string) (StudioPage, error) {
	var out struct {
		FindStudios struct {
			Count   int      `json:"count"`
			Studios []Studio `json:"studios"`
		} `json:"findStudios"`
	}
	vars := findVars(page, perPage, firstNonEmpty(sort, "name"), firstNonEmpty(direction, "ASC"), searchTerm)
	if err := c.query(ctx, listStudiosQuery, vars, &out); err != nil {
		if isInvalidSort(err) && sort != "name" {
			vars["find"].(map[string]any)["sort"] = "name"
			vars["find"].(map[string]any)["direction"] = "ASC"
			if retryErr := c.query(ctx, listStudiosQuery, vars, &out); retryErr != nil {
				return StudioPage{}, retryErr
			}
		} else {
			return StudioPage{}, err
		}
	}
	return StudioPage{Studios: out.FindStudios.Studios, Count: out.FindStudios.Count}, nil
}

func (c *Client) FindStudio(ctx context.Context, id string) (Studio, bool, error) {
	var out struct {
		FindStudio *Studio `json:"findStudio"`
	}
	if err := c.query(ctx, findStudioQuery, map[string]any{"id": id}, &out); err != nil {
		return Studio{}, false, err
	}
	if out.FindStudio == nil {
		return Studio{}, false, nil
	}
	return *out.FindStudio, true, nil
}

func (c *Client) ListTags(ctx context.Context, page, perPage int, searchTerm string) (TagPage, error) {
	return c.ListTagsBy(ctx, page, perPage, searchTerm, "name", "ASC")
}

func (c *Client) ListTagsBy(ctx context.Context, page, perPage int, searchTerm, sort, direction string) (TagPage, error) {
	return c.listTags(ctx, findVars(page, perPage, firstNonEmpty(sort, "name"), firstNonEmpty(direction, "ASC"), searchTerm), sort)
}

func (c *Client) ListTopTagsBy(ctx context.Context, page, perPage int, searchTerm, sort, direction string) (TagPage, error) {
	vars := findVars(page, perPage, firstNonEmpty(sort, "name"), firstNonEmpty(direction, "ASC"), searchTerm)
	vars["tag"] = map[string]any{"parent_count": intCriterion(0, "EQUALS")}
	return c.listTags(ctx, vars, sort)
}

func (c *Client) ListChildTagsBy(ctx context.Context, parentID string, page, perPage int, searchTerm, sort, direction string) (TagPage, error) {
	vars := findVars(page, perPage, firstNonEmpty(sort, "name"), firstNonEmpty(direction, "ASC"), searchTerm)
	vars["tag"] = map[string]any{"parents": criterion(parentID)}
	return c.listTags(ctx, vars, sort)
}

func (c *Client) listTags(ctx context.Context, vars map[string]any, sort string) (TagPage, error) {
	var out struct {
		FindTags struct {
			Count int   `json:"count"`
			Tags  []Tag `json:"tags"`
		} `json:"findTags"`
	}
	if err := c.query(ctx, listTagsQuery, vars, &out); err != nil {
		if isInvalidSort(err) && sort != "name" {
			vars["find"].(map[string]any)["sort"] = "name"
			vars["find"].(map[string]any)["direction"] = "ASC"
			if retryErr := c.query(ctx, listTagsQuery, vars, &out); retryErr != nil {
				return TagPage{}, retryErr
			}
		} else {
			return TagPage{}, err
		}
	}
	return TagPage{Tags: out.FindTags.Tags, Count: out.FindTags.Count}, nil
}

func (c *Client) FindTag(ctx context.Context, id string) (Tag, bool, error) {
	var out struct {
		FindTag *Tag `json:"findTag"`
	}
	if err := c.query(ctx, findTagQuery, map[string]any{"id": id}, &out); err != nil {
		return Tag{}, false, err
	}
	if out.FindTag == nil {
		return Tag{}, false, nil
	}
	return *out.FindTag, true, nil
}

func (c *Client) FindTagByName(ctx context.Context, name string) (Tag, bool, error) {
	page, err := c.ListTags(ctx, 1, 10, name)
	if err != nil {
		return Tag{}, false, err
	}
	for _, tag := range page.Tags {
		if strings.EqualFold(tag.Name, name) {
			return tag, true, nil
		}
	}
	if len(page.Tags) == 0 {
		return Tag{}, false, nil
	}
	return page.Tags[0], true, nil
}

func (c *Client) FindScene(ctx context.Context, id string) (Scene, bool, error) {
	var out struct {
		FindScene *Scene `json:"findScene"`
	}
	if err := c.query(ctx, findSceneQuery, map[string]any{"id": id}, &out); err != nil {
		return Scene{}, false, err
	}
	if out.FindScene == nil {
		return Scene{}, false, nil
	}
	return *out.FindScene, true, nil
}

func (c *Client) SaveSceneActivity(ctx context.Context, id string, resumeTime, playDuration float64) error {
	var out struct {
		SceneSaveActivity bool `json:"sceneSaveActivity"`
	}
	vars := map[string]any{
		"id":           id,
		"resume_time":  resumeTime,
		"playDuration": playDuration,
	}
	return c.query(ctx, saveSceneActivityMutation, vars, &out)
}

func (c *Client) AddScenePlay(ctx context.Context, id string) error {
	var out struct {
		SceneAddPlay struct {
			Count int `json:"count"`
		} `json:"sceneAddPlay"`
	}
	return c.query(ctx, addScenePlayMutation, map[string]any{"id": id}, &out)
}

func (c *Client) UpdateSceneRating(ctx context.Context, id string, rating100 int) error {
	var out struct {
		SceneUpdate struct {
			ID string `json:"id"`
		} `json:"sceneUpdate"`
	}
	return c.query(ctx, updateSceneRatingMutation, map[string]any{
		"input": map[string]any{
			"id":        id,
			"rating100": rating100,
		},
	}, &out)
}

func (c *Client) PublicURL(raw string) string {
	if raw == "" || c.publicURL == "" {
		return raw
	}

	source, err := url.Parse(raw)
	if err != nil || source.Scheme == "" || source.Host == "" {
		return raw
	}
	target, err := url.Parse(c.publicURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return raw
	}

	source.Scheme = target.Scheme
	source.Host = target.Host
	if target.Path != "" && target.Path != "/" {
		source.Path = strings.TrimRight(target.Path, "/") + source.Path
	}
	return source.String()
}

func (c *Client) GetURL(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("ApiKey", c.apiKey)
	return c.http.Do(req)
}

func (c *Client) query(ctx context.Context, query string, variables map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": variables,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("ApiKey", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("stash graphql returned %s", resp.Status)
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("stash graphql error: %s", envelope.Errors[0].Message)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("stash graphql returned no data")
	}
	return json.Unmarshal(envelope.Data, out)
}

func findVars(page, perPage int, sort, direction, searchTerm string) map[string]any {
	find := map[string]any{
		"page":      page,
		"per_page":  perPage,
		"sort":      sort,
		"direction": direction,
	}
	if searchTerm != "" {
		find["q"] = searchTerm
	}
	return map[string]any{"find": find}
}

func sceneFilterVars(filter SceneFilter) map[string]any {
	scene := map[string]any{}
	if filter.PerformerID != "" {
		scene["performers"] = criterion(filter.PerformerID)
	}
	if filter.StudioID != "" {
		scene["studios"] = hierarchicalCriterion(filter.StudioID)
	}
	if filter.TagID != "" {
		scene["tags"] = hierarchicalCriterion(filter.TagID)
	}
	return scene
}

func criterion(id string) map[string]any {
	return map[string]any{
		"value":    []string{id},
		"modifier": "INCLUDES",
	}
}

func intCriterion(value int, modifier string) map[string]any {
	return map[string]any{
		"value":    value,
		"modifier": modifier,
	}
}

func hierarchicalCriterion(id string) map[string]any {
	value := criterion(id)
	value["depth"] = -1
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func isInvalidSort(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid sort:")
}

const sceneFields = `
	id
	title
	code
	details
	director
	urls
	date
	rating100
	resume_time
	play_duration
	play_count
	paths {
		screenshot
		preview
		stream
		webp
		vtt
		sprite
		caption
	}
	sceneStreams {
		url
		mime_type
		label
	}
	files {
		path
		size
		format
		width
		height
		duration
		video_codec
		audio_codec
		frame_rate
		bit_rate
	}
	performers {
		id
		name
		image_path
	}
	studio {
		id
		name
		image_path
	}
	tags {
		id
		name
		sort_name
		image_path
	}
	stash_ids {
		endpoint
		stash_id
	}
`

const listScenesQuery = `
query ListScenes($find: FindFilterType!, $scene: SceneFilterType) {
	findScenes(filter: $find, scene_filter: $scene) {
		count
		scenes {
			` + sceneFields + `
		}
	}
}`

const findSceneQuery = `
query FindScene($id: ID!) {
	findScene(id: $id) {
		` + sceneFields + `
	}
}`

const saveSceneActivityMutation = `
mutation SaveSceneActivity($id: ID!, $resume_time: Float, $playDuration: Float) {
	sceneSaveActivity(id: $id, resume_time: $resume_time, playDuration: $playDuration)
}`

const addScenePlayMutation = `
mutation AddScenePlay($id: ID!) {
	sceneAddPlay(id: $id) {
		count
	}
}`

const updateSceneRatingMutation = `
mutation UpdateSceneRating($input: SceneUpdateInput!) {
	sceneUpdate(input: $input) {
		id
	}
}`

const performerFields = `
	id
	name
	disambiguation
	urls
	image_path
	scene_count
	details
	rating100
	favorite
`

const studioFields = `
	id
	name
	urls
	image_path
	scene_count
	details
	rating100
	favorite
`

const tagFields = `
	id
	name
	sort_name
	description
	aliases
	image_path
	scene_count
	parent_count
	child_count
	parents {
		id
		name
		sort_name
		image_path
		scene_count
		parent_count
		child_count
	}
	children {
		id
		name
		sort_name
		image_path
		scene_count
		parent_count
		child_count
	}
	favorite
`

const listPerformersQuery = `
query ListPerformers($find: FindFilterType!) {
	findPerformers(filter: $find) {
		count
		performers {
			` + performerFields + `
		}
	}
}`

const findPerformerQuery = `
query FindPerformer($id: ID!) {
	findPerformer(id: $id) {
		` + performerFields + `
	}
}`

const listStudiosQuery = `
query ListStudios($find: FindFilterType!) {
	findStudios(filter: $find) {
		count
		studios {
			` + studioFields + `
		}
	}
}`

const findStudioQuery = `
query FindStudio($id: ID!) {
	findStudio(id: $id) {
		` + studioFields + `
	}
}`

const listTagsQuery = `
query ListTags($find: FindFilterType!, $tag: TagFilterType) {
	findTags(filter: $find, tag_filter: $tag) {
		count
		tags {
			` + tagFields + `
		}
	}
}`

const findTagQuery = `
query FindTag($id: ID!) {
	findTag(id: $id) {
		` + tagFields + `
	}
}`
