package helperapp

import (
	"strings"
	"time"
)

func compactRange(start, end string) string {
	startText := compactDate(start)
	endText := compactDate(end)
	if startText == "-" && endText == "-" {
		return "-"
	}
	return startText + " ~ " + endText
}

func compactDate(value string) string {
	t, ok := parseTime(value)
	if !ok {
		return "-"
	}
	return t.Local().Format("2006-01-02")
}

func compactTime(value string) string {
	t, ok := parseTime(value)
	if !ok {
		return "未知"
	}
	now := time.Now()
	local := t.Local()
	y1, m1, d1 := now.Date()
	y2, m2, d2 := local.Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return "今天 " + local.Format("15:04")
	}
	tomorrow := now.Add(24 * time.Hour)
	y3, m3, d3 := tomorrow.Date()
	if y3 == y2 && m3 == m2 && d3 == d2 {
		return "明天 " + local.Format("15:04")
	}
	if local.Sub(now) > 0 && local.Sub(now) < 7*24*time.Hour {
		return "周" + weekdayName(local.Weekday()) + " " + local.Format("15:04")
	}
	return local.Format("01-02 15:04")
}

func parseTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		t, err := time.Parse(layout, value)
		if err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func weekdayName(day time.Weekday) string {
	switch day {
	case time.Monday:
		return "一"
	case time.Tuesday:
		return "二"
	case time.Wednesday:
		return "三"
	case time.Thursday:
		return "四"
	case time.Friday:
		return "五"
	case time.Saturday:
		return "六"
	default:
		return "日"
	}
}
