package kicad

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

type ImportSymbol struct {
	Name, Footprint, Extends string
	HasFootprint             bool
	Data                     []byte
}

type stringSpan struct {
	start, end int
	value      string
}

type byteSpan struct{ start, end int }

func LibraryVersion(data []byte) int {
	children, err := directExpressions(data)
	if err != nil || len(children) == 0 || expressionHead(data[children[0].start:children[0].end]) != "kicad_symbol_lib" {
		return 0
	}
	for _, child := range childrenOf(data, children[0]) {
		if expressionHead(data[child.start:child.end]) != "version" {
			continue
		}
		fields := strings.Fields(string(data[child.start:child.end]))
		if len(fields) < 2 {
			return 0
		}
		version, _ := strconv.Atoi(strings.TrimRight(fields[1], ")"))
		return version
	}
	return 0
}

func ImportSymbols(data []byte) ([]ImportSymbol, error) {
	roots, err := directExpressions(data)
	if err != nil || len(roots) == 0 || expressionHead(data[roots[0].start:roots[0].end]) != "kicad_symbol_lib" {
		return nil, fmt.Errorf("invalid symbol library")
	}
	spans, err := topSymbols(data)
	if err != nil {
		return nil, err
	}
	result := make([]ImportSymbol, 0, len(spans))
	for _, span := range spans {
		expression := data[span.start:span.end]
		property, err := directPropertySpan(expression, "Footprint")
		if err != nil {
			return nil, err
		}
		footprint, hasFootprint := "", property != nil
		if property != nil {
			footprint = property.value
		}
		extends, _, err := directStringProperty(expression, "extends")
		if err != nil {
			return nil, err
		}
		result = append(result, ImportSymbol{Name: span.name, Footprint: footprint, HasFootprint: hasFootprint, Extends: extends, Data: append([]byte(nil), expression...)})
	}
	return result, nil
}

func RewriteSymbolFootprints(data []byte, values map[string]string) ([]byte, error) {
	roots, rootErr := directExpressions(data)
	if rootErr == nil && len(roots) > 0 && expressionHead(data[roots[0].start:roots[0].end]) == "symbol" {
		name, nameErr := symbolName(data[roots[0].start:roots[0].end])
		if nameErr != nil {
			return nil, nameErr
		}
		if value, ok := values[name]; ok {
			found, propertyErr := directPropertySpan(data[roots[0].start:roots[0].end], "Footprint")
			if propertyErr != nil {
				return nil, propertyErr
			}
			if found != nil {
				return applyByteEdits(data, []byteEdit{{start: roots[0].start + found.start, end: roots[0].start + found.end, value: []byte(quotedContent(value))}}), nil
			}
		}
		return append([]byte(nil), data...), nil
	}
	spans, err := topSymbols(data)
	if err != nil {
		return nil, err
	}
	var edits []byteEdit
	for _, span := range spans {
		value, ok := values[span.name]
		if !ok {
			continue
		}
		found, err := directPropertySpan(data[span.start:span.end], "Footprint")
		if err != nil {
			return nil, err
		}
		if found == nil {
			continue
		}
		edits = append(edits, byteEdit{start: span.start + found.start, end: span.start + found.end, value: []byte(quotedContent(value))})
	}
	return applyByteEdits(data, edits), nil
}

func FootprintType(data []byte) bool {
	expressions, err := directExpressions(data)
	if err != nil || len(expressions) == 0 {
		return false
	}
	head := expressionHead(data[expressions[0].start:expressions[0].end])
	return head == "footprint" || head == "module"
}

func RewriteFootprintModels(data []byte, values map[string]string) ([]byte, int, error) {
	spans, err := modelStringSpans(data)
	if err != nil {
		return nil, 0, err
	}
	edits := make([]byteEdit, 0, len(spans))
	for _, span := range spans {
		if value, ok := values[span.value]; ok {
			edits = append(edits, byteEdit{start: span.start, end: span.end, value: []byte(quotedContent(value))})
		}
	}
	return applyByteEdits(data, edits), len(spans), nil
}

func InsertFootprintModel(data []byte, path string) ([]byte, bool) {
	spans, err := modelStringSpans(data)
	if err != nil || len(spans) != 0 {
		return append([]byte(nil), data...), false
	}
	end, ok := outerExpressionEnd(data)
	if !ok || !FootprintType(data) {
		return append([]byte(nil), data...), false
	}
	end--
	model := "\n  (model " + Quote(path) + " (offset (xyz 0 0 0)) (scale (xyz 1 1 1)) (rotate (xyz 0 0 0)))\n"
	out := make([]byte, 0, len(data)+len(model))
	out = append(out, data[:end]...)
	out = append(out, model...)
	out = append(out, data[end:]...)
	return out, true
}

func AppendSymbols(existing []byte, replacements map[string][]byte, appended [][]byte, version int) ([]byte, error) {
	if version < 20231120 {
		version = 20231120
	}
	if len(bytes.TrimSpace(existing)) == 0 {
		var out strings.Builder
		writeSymbolHeader(&out, version)
		for _, symbol := range appended {
			out.Write(symbol)
			out.WriteByte('\n')
		}
		out.WriteString(")\n")
		return []byte(out.String()), nil
	}
	spans, err := topSymbols(existing)
	if err != nil {
		return nil, err
	}
	if oldVersion := LibraryVersion(existing); oldVersion > version {
		version = oldVersion
	}
	var out strings.Builder
	writeSymbolHeader(&out, version)
	seen := make(map[string]bool, len(replacements))
	for _, span := range spans {
		if replacement, ok := replacements[span.name]; ok {
			out.Write(replacement)
			seen[span.name] = true
		} else {
			out.Write(existing[span.start:span.end])
		}
		out.WriteByte('\n')
	}
	for _, symbol := range appended {
		out.Write(symbol)
		out.WriteByte('\n')
	}
	for name, replacement := range replacements {
		if !seen[name] {
			out.Write(replacement)
			out.WriteByte('\n')
		}
	}
	out.WriteString(")\n")
	return []byte(out.String()), nil
}

func writeSymbolHeader(out *strings.Builder, version int) {
	out.WriteString("(kicad_symbol_lib (version ")
	out.WriteString(strconv.Itoa(version))
	out.WriteString(") (generator \"kicad-partfetch\") (generator_version \"0.1\")\n")
}

func quotedContent(value string) string { return Quote(value)[1 : len(Quote(value))-1] }

type byteEdit struct {
	start, end int
	value      []byte
}

func applyByteEdits(data []byte, edits []byteEdit) []byte {
	if len(edits) == 0 {
		return append([]byte(nil), data...)
	}
	var out []byte
	position := 0
	for _, edit := range edits {
		out = append(out, data[position:edit.start]...)
		out = append(out, edit.value...)
		position = edit.end
	}
	out = append(out, data[position:]...)
	return out
}

func directPropertySpan(expression []byte, name string) (*stringSpan, error) {
	for _, child := range childExpressions(expression) {
		form := expression[child.start:child.end]
		if expressionHead(form) != "property" || firstString(form) != name {
			continue
		}
		stringsFound := quotedStrings(form)
		if len(stringsFound) < 2 {
			return nil, fmt.Errorf("property %q value must be quoted", name)
		}
		return &stringSpan{start: child.start + stringsFound[1].start, end: child.start + stringsFound[1].end, value: stringsFound[1].value}, nil
	}
	return nil, nil
}

func directStringProperty(expression []byte, name string) (string, bool, error) {
	for _, child := range childExpressions(expression) {
		form := expression[child.start:child.end]
		if expressionHead(form) == name {
			stringsFound := quotedStrings(form)
			if len(stringsFound) > 0 {
				return stringsFound[0].value, true, nil
			}
			return "", false, fmt.Errorf("%s value must be quoted", name)
		}
	}
	return "", false, nil
}

func modelStringSpans(data []byte) ([]stringSpan, error) {
	spans := make([]stringSpan, 0)
	depth, quoted, escaped, comment := 0, false, false, false
	for i := 0; i < len(data); i++ {
		char := data[i]
		if comment {
			if char == '\n' || char == '\r' {
				comment = false
			}
			continue
		}
		if quoted {
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == '"' {
				quoted = false
			}
			continue
		}
		if char == ';' {
			comment = true
			continue
		}
		if char == '"' {
			quoted = true
			continue
		}
		if char == '(' {
			if depth > 0 && expressionHead(data[i:]) == "model" {
				end := expressionEnd(data, i)
				stringsFound := quotedStrings(data[i:end])
				if len(stringsFound) > 0 {
					spans = append(spans, stringSpan{start: i + stringsFound[0].start, end: i + stringsFound[0].end, value: stringsFound[0].value})
				}
			}
			depth++
		} else if char == ')' {
			depth--
		}
	}
	return spans, nil
}

func directExpressions(data []byte) ([]byteSpan, error) {
	return expressionsAtDepth(data, 0), nil
}

func childrenOf(data []byte, parent byteSpan) []byteSpan {
	return expressionsAtDepth(data[parent.start:parent.end], 1)
}

func childExpressions(data []byte) []byteSpan {
	return expressionsAtDepth(data, 1)
}

func expressionsAtDepth(data []byte, target int) []byteSpan {
	var spans []byteSpan
	depth, start := 0, -1
	quoted, escaped, comment := false, false, false
	for i, char := range data {
		if comment {
			if char == '\n' || char == '\r' {
				comment = false
			}
			continue
		}
		if quoted {
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == '"' {
				quoted = false
			}
			continue
		}
		if char == ';' {
			comment = true
			continue
		}
		if char == '"' {
			quoted = true
			continue
		}
		if char == '(' {
			if depth == target {
				start = i
			}
			depth++
		} else if char == ')' {
			depth--
			if depth == target && start >= 0 {
				spans = append(spans, byteSpan{start: start, end: i + 1})
				start = -1
			}
		}
	}
	return spans
}

func expressionHead(expression []byte) string {
	i := 0
	for i < len(expression) && expression[i] != '(' {
		i++
	}
	i++
	for i < len(expression) && isSpace(expression[i]) {
		i++
	}
	start := i
	for i < len(expression) && !isSpace(expression[i]) && expression[i] != ')' {
		i++
	}
	return string(expression[start:i])
}

func firstString(expression []byte) string {
	stringsFound := quotedStrings(expression)
	if len(stringsFound) == 0 {
		return ""
	}
	return stringsFound[0].value
}

func quotedStrings(expression []byte) []stringSpan {
	var spans []stringSpan
	quoted, escaped := false, false
	start := -1
	var value strings.Builder
	for i, char := range expression {
		if !quoted {
			if char == '"' {
				quoted, start = true, i+1
				value.Reset()
			}
			continue
		}
		if escaped {
			value.WriteByte(byte(char))
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if char == '"' {
			spans = append(spans, stringSpan{start: start, end: i, value: value.String()})
			quoted = false
			continue
		}
		value.WriteByte(byte(char))
	}
	return spans
}

func outerExpressionEnd(data []byte) (int, bool) {
	spans, err := directExpressions(data)
	if err != nil || len(spans) == 0 {
		return 0, false
	}
	return spans[0].end, true
}

func expressionEnd(data []byte, start int) int {
	depth, quoted, escaped := 0, false, false
	for i := start; i < len(data); i++ {
		char := data[i]
		if quoted {
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == '"' {
				quoted = false
			}
			continue
		}
		if char == '"' {
			quoted = true
		} else if char == '(' {
			depth++
		} else if char == ')' {
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(data)
}

func isSpace(char byte) bool { return char == ' ' || char == '\t' || char == '\r' || char == '\n' }
