package store

import "encoding/json"

// jsonMarshal / jsonUnmarshal wrap stdlib for easy future swap to msgpack.
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
