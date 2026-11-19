package sanitizer

import (
	"fmt"
	"os/exec"
	"strings"
)

// RequiredTools are the host programs sanitizing relies on.
var RequiredTools = []string{"pdftoppm", "ffmpeg", "ffprobe"}

// CheckTools returns an error naming every missing program, so an app can
// refuse to start (as opposed to failing on the first upload that needs one).
func CheckTools(names ...string) error {
	if len(names) == 0 {
		names = RequiredTools
	}
	var missing []string
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing host tools: %s (install poppler-utils and ffmpeg)", strings.Join(missing, ", "))
	}
	return nil
}
