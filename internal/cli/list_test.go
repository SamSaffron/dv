package cli

import (
	"reflect"
	"testing"
)

func TestParseLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected map[string]string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: map[string]string{},
		},
		{
			name:     "whitespace only",
			input:    "   ",
			expected: map[string]string{},
		},
		{
			name:     "single label",
			input:    "key=value",
			expected: map[string]string{"key": "value"},
		},
		{
			name:     "multiple labels",
			input:    "key1=value1,key2=value2,key3=value3",
			expected: map[string]string{"key1": "value1", "key2": "value2", "key3": "value3"},
		},
		{
			name:     "labels with spaces around commas",
			input:    "key1=value1 , key2=value2",
			expected: map[string]string{"key1": "value1", "key2": "value2"},
		},
		{
			name:     "labels with spaces around equals",
			input:    "key = value",
			expected: map[string]string{"key": "value"},
		},
		{
			name:     "value with equals sign",
			input:    "url=https://example.com?foo=bar",
			expected: map[string]string{"url": "https://example.com?foo=bar"},
		},
		{
			name:     "value with multiple equals signs",
			input:    "equation=a=b=c",
			expected: map[string]string{"equation": "a=b=c"},
		},
		{
			name:     "empty value",
			input:    "key=",
			expected: map[string]string{"key": ""},
		},
		{
			name:     "malformed entry no equals",
			input:    "noequals",
			expected: map[string]string{},
		},
		{
			name:     "mixed valid and malformed",
			input:    "valid=yes,malformed,also=valid",
			expected: map[string]string{"valid": "yes", "also": "valid"},
		},
		{
			name:     "empty entries in sequence",
			input:    "key1=value1,,key2=value2",
			expected: map[string]string{"key1": "value1", "key2": "value2"},
		},
		{
			name:     "trailing comma",
			input:    "key=value,",
			expected: map[string]string{"key": "value"},
		},
		{
			name:     "leading comma",
			input:    ",key=value",
			expected: map[string]string{"key": "value"},
		},
		{
			name:     "unicode in values",
			input:    "greeting=héllo,name=世界",
			expected: map[string]string{"greeting": "héllo", "name": "世界"},
		},
		{
			name:     "docker label format example",
			input:    "com.dv.owner=dv,com.dv.image-name=discourse,com.dv.image-tag=latest",
			expected: map[string]string{"com.dv.owner": "dv", "com.dv.image-name": "discourse", "com.dv.image-tag": "latest"},
		},
		{
			name:     "empty key ignored",
			input:    "=value",
			expected: map[string]string{},
		},
		{
			name:     "whitespace key ignored",
			input:    "  =value",
			expected: map[string]string{},
		},
		{
			name:     "duplicate keys last wins",
			input:    "key=first,key=second",
			expected: map[string]string{"key": "second"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseLabels(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("parseLabels(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
