package imagejob

import (
	"fmt"
	"path/filepath"
	"strings"
)

func ClassifyOutputs(outputs map[string]any, jobID string) []MediaOutput {
	var out []MediaOutput
	index := 0
	for node, v := range outputs {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		classType := fmt.Sprint(m["class_type"])
		if classType == "<nil>" {
			classType = ""
		}
		for key, val := range m {
			for _, x := range outputEntries(val) {
				name := fmt.Sprint(x["filename"])
				if name == "<nil>" {
					name = ""
				}
				mime := fmt.Sprint(x["mime"])
				if mime == "<nil>" || mime == "" {
					mime = fmt.Sprint(x["content_type"])
				}
				if mime == "<nil>" {
					mime = ""
				}
				if mime == "" {
					mime = MIMEFromName(name)
				}
				kind := OutputKind(name, mime, classType)
				if kind == "" {
					continue
				}
				out = append(out, MediaOutput{Kind: kind, NodeID: node, OutputKey: key, ClassType: classType, MIME: mime, Previewable: kind == "image", URL: fmt.Sprintf("/api/v2/image-jobs/%s/outputs/%d", jobID, index)})
				index++
			}
		}
	}
	return out
}

func MIMEFromName(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".mp4":
		return "video/mp4"
	case ".wav":
		return "audio/wav"
	}
	return "application/octet-stream"
}

func OutputKind(name, mime, classType string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if strings.HasPrefix(mime, "image/") {
		return "image"
	}
	if strings.HasPrefix(mime, "video/") {
		return "video"
	}
	if strings.HasPrefix(mime, "audio/") {
		return "audio"
	}
	s := strings.ToLower(name + " " + mime + " " + classType)
	switch {
	case strings.Contains(s, "image") || isImageName(name):
		return "image"
	case strings.Contains(s, "video") || strings.HasSuffix(s, ".mp4"):
		return "video"
	case strings.Contains(s, "audio") || strings.HasSuffix(s, ".wav"):
		return "audio"
	case strings.Contains(s, "text") || strings.Contains(s, "string"):
		return "text"
	default:
		return "file"
	}
}

func outputEntries(raw any) []map[string]any {
	switch arr := raw.(type) {
	case []any:
		out := make([]map[string]any, 0, len(arr))
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case []map[string]any:
		return arr
	default:
		return nil
	}
}

func isImageName(name string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(name))) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp", ".tif", ".tiff":
		return true
	default:
		return false
	}
}
