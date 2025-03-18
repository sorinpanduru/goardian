package web

import (
	"fmt"
	"math"
)

// FormatUptime converts seconds to a human-readable uptime string
func FormatUptime(seconds float64) string {
	if seconds < 60 {
		return fmt.Sprintf("%.0f sec", seconds)
	} else if seconds < 3600 {
		return fmt.Sprintf("%.0f min %.0f sec", seconds/60, math.Mod(seconds, 60))
	} else if seconds < 86400 {
		hours := seconds / 3600
		minutes := math.Mod(seconds, 3600) / 60
		return fmt.Sprintf("%.0f hr %.0f min", hours, minutes)
	} else {
		days := seconds / 86400
		hours := math.Mod(seconds, 86400) / 3600
		return fmt.Sprintf("%.0f days %.0f hr", days, hours)
	}
}

// FormatMemorySize converts bytes to a human-readable memory size string
func FormatMemorySize(bytes uint64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	if bytes < KB {
		return fmt.Sprintf("%d B", bytes)
	} else if bytes < MB {
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	} else if bytes < GB {
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	} else {
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	}
}
