package workspace

import "encoding/json"

// jsonMarshalIndent wraps encoding/json.MarshalIndent with two-space
// indent — keeps the co_changes.json file readable.
func jsonMarshalIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// jsonUnmarshal wraps encoding/json.Unmarshal so cochange.go doesn't
// have to import encoding/json directly.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
