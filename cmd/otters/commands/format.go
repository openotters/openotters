package commands

import "fmt"

// humanSize renders a byte count using binary (1024-based) units so
// that sub-KiB values keep their exact byte count and larger values
// collapse to a short label like "12.3 MiB".
func humanSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	suffix := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}[exp]

	return fmt.Sprintf("%.1f %s", float64(bytes)/float64(div), suffix)
}
