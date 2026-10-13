package easyeda

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func decode(data []byte) (map[string]any, error) {
	var v map[string]any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func list(v any) []any         { a, _ := v.([]any); return a }
func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return ""
	}
}
func num(v any) (float64, error) {
	s := strings.TrimSpace(str(v))
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q: %w", s, err)
	}
	return n, nil
}
func field(m map[string]any, key string) (float64, error) { return num(m[key]) }
func numberAt(a []string, i int) (float64, error) {
	if i >= len(a) {
		return 0, nil
	}
	if strings.TrimSpace(a[i]) == "" {
		return 0, nil
	}
	return strconv.ParseFloat(strings.TrimSpace(a[i]), 64)
}

func numbersAt(a []string, indexes ...int) ([]float64, error) {
	values := make([]float64, len(indexes))
	for i, index := range indexes {
		value, err := numberAt(a, index)
		if err != nil {
			return nil, err
		}
		values[i] = value
	}
	return values, nil
}
