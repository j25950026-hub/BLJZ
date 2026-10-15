package bot

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type workHoursWindow struct {
	startSeconds int
	endSeconds   int
	startText    string
	endText      string
}

func ResolveWorkHoursWindow(startRaw, endRaw string) (string, bool) {
	window, ok := parseWorkHoursWindow(startRaw, endRaw)
	if !ok {
		return "", false
	}
	return window.label(), true
}

func parseWorkHoursWindow(startRaw, endRaw string) (workHoursWindow, bool) {
	startSeconds, startText, ok := parseClockSecond(startRaw)
	if !ok {
		return workHoursWindow{}, false
	}
	endSeconds, endText, ok := parseClockSecond(endRaw)
	if !ok {
		return workHoursWindow{}, false
	}
	return workHoursWindow{
		startSeconds: startSeconds,
		endSeconds:   endSeconds,
		startText:    startText,
		endText:      endText,
	}, true
}

func parseClockSecond(raw string) (int, string, bool) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, "", false
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", false
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, "", false
	}
	s := 0
	if len(parts) == 3 {
		s, err = strconv.Atoi(parts[2])
		if err != nil {
			return 0, "", false
		}
	}
	if h < 0 || h > 23 || m < 0 || m > 59 || s < 0 || s > 59 {
		return 0, "", false
	}
	return h*3600 + m*60 + s, fmt.Sprintf("%02d:%02d:%02d", h, m, s), true
}

func (w workHoursWindow) contains(at time.Time) bool {
	current := at.Hour()*3600 + at.Minute()*60 + at.Second()
	if w.startSeconds == w.endSeconds {
		return true
	}
	if w.startSeconds < w.endSeconds {
		return current >= w.startSeconds && current <= w.endSeconds
	}
	return current >= w.startSeconds || current <= w.endSeconds
}

func (w workHoursWindow) label() string {
	return w.startText + "-" + w.endText
}
