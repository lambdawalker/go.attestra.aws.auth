package runtimeconfig

import (
	"bytes"
	"encoding/json"
)

// Omitted onboarding is the legacy contract. An explicit null is malformed,
// not a request to reinterpret the document as an older policy.
func (c *Config) UnmarshalJSON(data []byte) error {
	type plain Config
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if value, present := raw["onboarding"]; present && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return ErrInvalid
	}
	var result plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return err
	}
	*c = Config(result)
	return nil
}
