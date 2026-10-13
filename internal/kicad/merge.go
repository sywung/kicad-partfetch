package kicad

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// MergeSymbols replaces selected top-level symbol expressions while retaining other expressions byte for byte.
func MergeSymbols(existing []byte, replacements map[string][]byte) ([]byte, error) {
	if len(bytes.TrimSpace(existing)) == 0 {
		return NewSymbolLibrary(replacements)
	}
	spans, e := topSymbols(existing)
	if e != nil {
		return nil, e
	}
	version := LibraryVersion(existing)
	if version < 20231120 {
		version = 20231120
	}
	seen := map[string]bool{}
	var out strings.Builder
	fmt.Fprintf(&out, "(kicad_symbol_lib (version %d) (generator \"kicad-partfetch\") (generator_version \"0.1\")\n", version)
	for _, span := range spans {
		name := span.name
		if replacement, ok := replacements[name]; ok {
			out.Write(replacement)
			seen[name] = true
		} else {
			out.Write(existing[span.start:span.end])
		}
		out.WriteByte('\n')
	}
	names := make([]string, 0, len(replacements))
	for name := range replacements {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		replacement := replacements[name]
		if !seen[name] {
			out.Write(replacement)
			out.WriteByte('\n')
		}
	}
	out.WriteString(")\n")
	return []byte(out.String()), nil
}

func NewSymbolLibrary(symbols map[string][]byte) ([]byte, error) {
	var out strings.Builder
	out.WriteString("(kicad_symbol_lib (version 20231120) (generator \"kicad-partfetch\") (generator_version \"0.1\")\n")
	names := make([]string, 0, len(symbols))
	for name := range symbols {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out.Write(symbols[name])
		out.WriteByte('\n')
	}
	out.WriteString(")\n")
	return []byte(out.String()), nil
}

func SymbolNames(data []byte) ([]string, error) {
	spans, e := topSymbols(data)
	if e != nil {
		return nil, e
	}
	names := make([]string, len(spans))
	for i, s := range spans {
		names[i] = s.name
	}
	return names, nil
}

// RemoveSymbols removes named top-level symbol expressions while preserving every
// other byte in the symbol library.
func RemoveSymbols(existing []byte, names []string) ([]byte, []string, error) {
	spans, err := topSymbols(existing)
	if err != nil {
		return nil, nil, err
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	type removal struct{ start, end int }
	var removals []removal
	removed := make([]string, 0)
	for _, span := range spans {
		if !wanted[span.name] {
			continue
		}
		start, end := span.start, span.end
		for start > 0 && (existing[start-1] == ' ' || existing[start-1] == '\t') {
			start--
		}
		if end < len(existing) && existing[end] == '\r' && end+1 < len(existing) && existing[end+1] == '\n' {
			end += 2
		} else if end < len(existing) && existing[end] == '\n' {
			end++
		}
		removals = append(removals, removal{start: start, end: end})
		removed = append(removed, span.name)
	}
	if len(removals) == 0 {
		return append([]byte(nil), existing...), []string{}, nil
	}
	out := make([]byte, 0, len(existing))
	position := 0
	for _, removal := range removals {
		if removal.start < position {
			removal.start = position
		}
		out = append(out, existing[position:removal.start]...)
		position = removal.end
	}
	out = append(out, existing[position:]...)
	return out, removed, nil
}

// FootprintModelPaths returns decoded model paths in footprint expression order.
func FootprintModelPaths(data []byte) []string {
	paths := make([]string, 0)
	scan := symbolScan{start: -1}
	for i := 0; i < len(data); i++ {
		if scan.comment {
			if data[i] == '\n' || data[i] == '\r' {
				scan.comment = false
			}
			continue
		}
		if scan.inString(data[i]) {
			continue
		}
		if data[i] == '"' {
			scan.quoted = true
			continue
		}
		if data[i] == ';' {
			scan.comment = true
			continue
		}
		if data[i] == '(' {
			if path, ok := modelPathAt(data, i); ok {
				paths = append(paths, path)
			}
		}
	}
	return paths
}

func modelPathAt(data []byte, start int) (string, bool) {
	i := start + 1
	for i < len(data) && (data[i] == ' ' || data[i] == '\t' || data[i] == '\r' || data[i] == '\n') {
		i++
	}
	if !bytes.HasPrefix(data[i:], []byte("model")) {
		return "", false
	}
	i += len("model")
	if i < len(data) && data[i] != ' ' && data[i] != '\t' && data[i] != '\r' && data[i] != '\n' {
		return "", false
	}
	for i < len(data) && (data[i] == ' ' || data[i] == '\t' || data[i] == '\r' || data[i] == '\n') {
		i++
	}
	if i >= len(data) || data[i] != '"' {
		return "", false
	}
	i++
	var path strings.Builder
	for ; i < len(data); i++ {
		if data[i] == '\\' {
			i++
			if i >= len(data) {
				return "", false
			}
			path.WriteByte(data[i])
			continue
		}
		if data[i] == '"' {
			return path.String(), true
		}
		path.WriteByte(data[i])
	}
	return "", false
}

type symbolSpan struct {
	name       string
	start, end int
}

// LibrarySymbol exposes source identifiers and footprint fields used by library inventory.
type LibrarySymbol struct {
	Name, Footprint, LCSC, EasyEDA string
}

// SymbolLibraryInfo reads selected properties from top-level symbol entries.
func SymbolLibraryInfo(data []byte) ([]LibrarySymbol, error) {
	spans, err := topSymbols(data)
	if err != nil {
		return nil, err
	}
	result := make([]LibrarySymbol, 0, len(spans))
	for _, span := range spans {
		expression := data[span.start:span.end]
		footprint, err := propertyValue(expression, "Footprint")
		if err != nil {
			return nil, err
		}
		lcsc, err := propertyValue(expression, "LCSC")
		if err != nil {
			return nil, err
		}
		easyeda, err := propertyValue(expression, "EasyEDA")
		if err != nil {
			return nil, err
		}
		result = append(result, LibrarySymbol{Name: span.name, Footprint: footprint, LCSC: lcsc, EasyEDA: easyeda})
	}
	return result, nil
}

func propertyValue(expression []byte, property string) (string, error) {
	needle := []byte("(property " + Quote(property) + " ")
	index := bytes.Index(expression, needle)
	if index < 0 {
		return "", nil
	}
	index += len(needle)
	if index >= len(expression) || expression[index] != '"' {
		return "", fmt.Errorf("property %q value must be quoted", property)
	}
	index++
	var value strings.Builder
	escaped := false
	for ; index < len(expression); index++ {
		current := expression[index]
		if escaped {
			value.WriteByte(current)
			escaped = false
			continue
		}
		if current == '\\' {
			escaped = true
			continue
		}
		if current == '"' {
			return value.String(), nil
		}
		value.WriteByte(current)
	}
	return "", fmt.Errorf("unterminated %q property value", property)
}

func topSymbols(data []byte) ([]symbolSpan, error) {
	scan := symbolScan{start: -1}
	foundExpression := false
	for i := 0; i < len(data); i++ {
		if !scan.comment && !scan.quoted && data[i] == '(' {
			foundExpression = true
		}
		if e := scan.consume(data, i); e != nil {
			return nil, e
		}
	}
	if !foundExpression {
		return nil, fmt.Errorf("invalid symbol library")
	}
	if scan.depth != 0 || scan.quoted {
		return nil, fmt.Errorf("unterminated expression in symbol library")
	}
	return scan.spans, nil
}

type symbolScan struct {
	depth, start             int
	quoted, escaped, topName bool
	comment                  bool
	spans                    []symbolSpan
}

func (s *symbolScan) consume(data []byte, index int) error {
	c := data[index]
	if s.comment {
		if c == '\n' || c == '\r' {
			s.comment = false
		}
		return nil
	}
	if s.inString(c) {
		return nil
	}
	if c == '"' {
		s.quoted = true
		return nil
	}
	if c == ';' {
		s.comment = true
		return nil
	}
	if c == '(' {
		if s.depth == 1 {
			s.start = index
			s.topName = bytes.HasPrefix(bytes.TrimSpace(data[index:]), []byte("(symbol "))
		}
		s.depth++
		return nil
	}
	if c == ')' {
		s.depth--
		if s.depth < 0 {
			return fmt.Errorf("unbalanced symbol library")
		}
		if s.depth == 1 && s.start >= 0 {
			return s.finish(data, index)
		}
	}
	return nil
}

func (s *symbolScan) inString(c byte) bool {
	if !s.quoted {
		return false
	}
	if s.escaped {
		s.escaped = false
	} else if c == '\\' {
		s.escaped = true
	} else if c == '"' {
		s.quoted = false
	}
	return true
}
func (s *symbolScan) finish(data []byte, index int) error {
	if s.topName {
		name, e := symbolName(data[s.start : index+1])
		if e != nil {
			return e
		}
		s.spans = append(s.spans, symbolSpan{name: name, start: s.start, end: index + 1})
	}
	s.start = -1
	return nil
}

func symbolName(expr []byte) (string, error) {
	i := bytes.Index(expr, []byte("(symbol"))
	if i < 0 {
		return "", fmt.Errorf("malformed symbol expression")
	}
	i += len("(symbol")
	for i < len(expr) && (expr[i] == ' ' || expr[i] == '\n' || expr[i] == '\t') {
		i++
	}
	if i >= len(expr) || expr[i] != '"' {
		return "", fmt.Errorf("symbol name must be a string")
	}
	i++
	var b strings.Builder
	escaped := false
	for ; i < len(expr); i++ {
		c := expr[i]
		if escaped {
			b.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			return b.String(), nil
		}
		b.WriteByte(c)
	}
	return "", fmt.Errorf("unterminated symbol name")
}
