package model

import "testing"

func TestResponseAllowsAdditiveFieldsButValidatesJSON(t *testing.T) {
	var value struct {
		Value int `json:"value"`
	}
	if err := DecodeResponse([]byte(`{"value":3,"future":{"enabled":true}}`), &value); err != nil || value.Value != 3 {
		t.Fatalf("additive response: %v", err)
	}
	for _, data := range []string{`{"value":"bad"}`, `{"value":3} {}`, `{"value":3} trailing`} {
		if DecodeResponse([]byte(data), &value) == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	if DecodeStrict([]byte(`{"value":3,"future":true}`), &value) == nil {
		t.Fatal("strict decoder was weakened")
	}
}
