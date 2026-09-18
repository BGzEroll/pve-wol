package pve

import (
	"encoding/json"
	"testing"
)

func TestResourceIsTemplate(t *testing.T) {
	tests := []struct {
		name     string
		template string
		want     bool
	}{
		{name: "pve integer true", template: "1", want: true},
		{name: "boolean true", template: "true", want: true},
		{name: "integer false", template: "0", want: false},
		{name: "boolean false", template: "false", want: false},
		{name: "missing", template: "", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resource := Resource{}
			if test.template != "" {
				resource.Template = json.RawMessage(test.template)
			}
			if got := resource.IsTemplate(); got != test.want {
				t.Fatalf("IsTemplate() = %v, want %v", got, test.want)
			}
		})
	}
}
