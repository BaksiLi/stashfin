package stash

type Scene struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Code         string           `json:"code"`
	Details      string           `json:"details"`
	Director     string           `json:"director"`
	URLs         []string         `json:"urls"`
	Date         string           `json:"date"`
	CreatedAt    string           `json:"created_at"`
	Rating100    int              `json:"rating100"`
	ResumeTime   float64          `json:"resume_time"`
	PlayDuration float64          `json:"play_duration"`
	PlayCount    int              `json:"play_count"`
	Paths        ScenePaths       `json:"paths"`
	SceneStreams []StreamEndpoint `json:"sceneStreams"`
	Files        []VideoFile      `json:"files"`
	Performers   []Performer      `json:"performers"`
	Studio       *Studio          `json:"studio"`
	Tags         []Tag            `json:"tags"`
	StashIDs     []StashID        `json:"stash_ids"`
}

type ScenePaths struct {
	Screenshot string `json:"screenshot"`
	Preview    string `json:"preview"`
	Stream     string `json:"stream"`
	WebP       string `json:"webp"`
	VTT        string `json:"vtt"`
	Sprite     string `json:"sprite"`
	Caption    string `json:"caption"`
}

type StreamEndpoint struct {
	URL      string `json:"url"`
	MIMEType string `json:"mime_type"`
	Label    string `json:"label"`
}

type VideoFile struct {
	Path       string  `json:"path"`
	Size       int64   `json:"size"`
	Format     string  `json:"format"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	Duration   float64 `json:"duration"`
	VideoCodec string  `json:"video_codec"`
	AudioCodec string  `json:"audio_codec"`
	FrameRate  float64 `json:"frame_rate"`
	BitRate    int64   `json:"bit_rate"`
}

type Performer struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Disambiguation string   `json:"disambiguation"`
	URLs           []string `json:"urls"`
	ImagePath      string   `json:"image_path"`
	SceneCount     int      `json:"scene_count"`
	Details        string   `json:"details"`
	Rating100      int      `json:"rating100"`
	Favorite       bool     `json:"favorite"`
}

type Studio struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	URLs       []string `json:"urls"`
	ImagePath  string   `json:"image_path"`
	SceneCount int      `json:"scene_count"`
	Details    string   `json:"details"`
	Rating100  int      `json:"rating100"`
	Favorite   bool     `json:"favorite"`
}

type Tag struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	SortName    string   `json:"sort_name"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
	ImagePath   string   `json:"image_path"`
	SceneCount  int      `json:"scene_count"`
	ParentCount int      `json:"parent_count"`
	ChildCount  int      `json:"child_count"`
	Parents     []Tag    `json:"parents"`
	Children    []Tag    `json:"children"`
	Favorite    bool     `json:"favorite"`
}

type StashID struct {
	Endpoint string `json:"endpoint"`
	StashID  string `json:"stash_id"`
}

type ScenePage struct {
	Scenes []Scene
	Count  int
}

type PerformerPage struct {
	Performers []Performer
	Count      int
}

type StudioPage struct {
	Studios []Studio
	Count   int
}

type TagPage struct {
	Tags  []Tag
	Count int
}
