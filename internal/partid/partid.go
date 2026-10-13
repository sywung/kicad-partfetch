// Package partid parses canonical identifiers for supported component sources.
package partid

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	lcscPattern    = regexp.MustCompile(`(?i)^C\d+$`)
	easyedaPattern = regexp.MustCompile(`^easyeda:([0-9a-fA-F]{32})$`)
	githubPattern  = regexp.MustCompile(`^github:([A-Za-z0-9._-]{1,100})/([A-Za-z0-9._-]{1,100})$`)
)

// Parse validates a PartID and returns its source and canonical value.
func Parse(s string) (source, value string, err error) {
	if lcscPattern.MatchString(s) {
		return "lcsc", strings.ToUpper(s), nil
	}
	if match := easyedaPattern.FindStringSubmatch(s); match != nil {
		return "easyeda", strings.ToLower(match[1]), nil
	}
	if match := githubPattern.FindStringSubmatch(s); match != nil && match[1] != "." && match[1] != ".." && match[2] != "." && match[2] != ".." {
		return "github", match[1] + "/" + match[2], nil
	}
	return "", "", fmt.Errorf("invalid part ID %q", s)
}
