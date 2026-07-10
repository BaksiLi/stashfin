package jellyfin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const maxJSONBodyBytes = 1 << 20

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "JSON body too large"})
			return false
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return false
	}
	return true
}
