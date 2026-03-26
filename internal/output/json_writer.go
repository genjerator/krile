package output

import (
	"encoding/json"
	"io"

	"github.com/genjerator/krile/internal/models"
)

// JSONWriter writes one JSON object per line (NDJSON).
type JSONWriter struct {
	enc *json.Encoder
}

func NewJSONWriter(w io.Writer) *JSONWriter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &JSONWriter{enc: enc}
}

func (w *JSONWriter) Write(businesses []models.Business) (int, error) {
	for _, b := range businesses {
		if err := w.enc.Encode(b); err != nil {
			return 0, err
		}
	}
	return len(businesses), nil
}

func (w *JSONWriter) Flush() error { return nil }
