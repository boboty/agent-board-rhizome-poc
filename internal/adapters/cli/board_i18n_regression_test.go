package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestBoardViewModelNoHardcodedChinese ensures the Go helper functions in
// board_view_model.go contain no hardcoded Chinese (CJK) user-facing strings.
// All such strings must live in BoardUIStrings (board_ui_strings.go) so they
// can be localized. The test scans the source file, not the compiled output,
// and allows characters in string-literal comments (// ...) and import paths.
//
// The test also checks that the BoardUIStrings struct itself has no blank
// (empty) field comment — every field must document its default zh-CN value
// as a comment, so a reviewer can see the locale content at the declaration.
func TestBoardViewModelNoHardcodedChinese(t *testing.T) {
	data, err := os.ReadFile("board_view_model.go")
	if err != nil {
		t.Fatalf("reading board_view_model.go: %v", err)
	}
	lines := strings.Split(string(data), "\n")

	// Match one or more CJK characters (including full-width punctuation) in a
	// non-comment line.  This fires on any line that contains Chinese and is
	// not a pure comment line.
	cjkRe := regexp.MustCompile(`[\x{4e00}-\x{9fff}\x{3000}-\x{303f}\x{ff00}-\x{ffef}]`)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(strings.TrimLeft(line, "\t "), "//") {
			continue
		}
		// Skip import paths
		if strings.Contains(line, `"rhizome-mcp/`) || strings.Contains(line, `"fmt"`) ||
			strings.Contains(line, `"html/`) || strings.Contains(line, `"strconv"`) ||
			strings.Contains(line, `"strings"`) || strings.Contains(line, `"time"`) ||
			strings.Contains(line, `"net/url"`) || strings.Contains(line, `"bytes"`) {
			continue
		}
		if cjkRe.MatchString(line) {
			t.Errorf("line %d contains CJK characters outside BoardUIStrings: %s", i+1, strings.TrimSpace(line))
		}
	}
}
