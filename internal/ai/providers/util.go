package providers

import (
	"encoding/json"
	"fmt"
	"strings"
)

func stringValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case json.Number:
		return val.String()
	case fmt.Stringer:
		return val.String()
	default:
		return ""
	}
}

func floatValue(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case json.Number:
		f, _ := val.Float64()
		return f
	case string:
		var out float64
		fmt.Sscanf(strings.TrimSpace(val), "%f", &out)
		return out
	default:
		return 0
	}
}
