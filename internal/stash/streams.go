package stash

import "strings"

func BestStream(scene Scene) string {
	for _, candidate := range scene.SceneStreams {
		if strings.EqualFold(candidate.Label, "Direct stream") && candidate.URL != "" {
			return candidate.URL
		}
	}
	for _, candidate := range scene.SceneStreams {
		if candidate.URL == "" {
			continue
		}
		if strings.EqualFold(candidate.MIMEType, "video/mp4") && strings.EqualFold(candidate.Label, "MP4") {
			return candidate.URL
		}
	}
	for _, candidate := range scene.SceneStreams {
		if candidate.URL == "" {
			continue
		}
		if strings.EqualFold(candidate.MIMEType, "application/vnd.apple.mpegurl") {
			return candidate.URL
		}
	}
	if scene.Paths.Stream != "" {
		return scene.Paths.Stream
	}
	if len(scene.SceneStreams) > 0 {
		return scene.SceneStreams[0].URL
	}
	return ""
}
