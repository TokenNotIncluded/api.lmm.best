package dto

import "encoding/json"

func (m MessageImageUrl) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Url      string `json:"url"`
		Detail   string `json:"detail,omitempty"`
		MimeType string `json:"mime_type,omitempty"`
	}{
		Url:      m.Url,
		Detail:   m.Detail,
		MimeType: m.MimeType,
	})
}
