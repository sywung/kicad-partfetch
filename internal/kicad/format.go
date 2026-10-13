// Package kicad serializes source-neutral components as KiCad 8 library data.
package kicad

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

func Quote(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
func Number(v float64) string {
	if math.Abs(v) < 0.00005 {
		v = 0
	}
	s := strconv.FormatFloat(v, 'f', 4, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}
func Point(x, y float64) string { return Number(x) + " " + Number(y) }
func Stroke(width float64) string {
	return fmt.Sprintf("(stroke (width %s) (type default))", Number(width))
}

func Sanitize(name string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\\:*?"<>|`, r) || unicode.IsSpace(r) {
			return '_'
		}
		return r
	}, name)
}
