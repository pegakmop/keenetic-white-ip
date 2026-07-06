package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Clock struct {
	Minutes int
}

func parseClock(s string) (Clock, error) {
	hh, mm, ok := strings.Cut(s, ":")
	if !ok {
		return Clock{}, fmt.Errorf("invalid time %q, expected HH:MM", s)
	}
	h, err := strconv.Atoi(hh)
	if err != nil || h < 0 || h > 23 {
		return Clock{}, fmt.Errorf("invalid hour in %q", s)
	}
	m, err := strconv.Atoi(mm)
	if err != nil || m < 0 || m > 59 {
		return Clock{}, fmt.Errorf("invalid minute in %q", s)
	}
	return Clock{Minutes: h*60 + m}, nil
}

func (c Clock) String() string {
	return fmt.Sprintf("%02d:%02d", c.Minutes/60, c.Minutes%60)
}

func parseUTCOffset(s string) (int, error) {
	hours, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("invalid utc_offset %q, expected format like +3 or -1", s)
	}
	if hours < -12 || hours > 14 {
		return 0, fmt.Errorf("utc_offset %q out of range [-12, 14]", s)
	}
	return hours, nil
}

type Window struct {
	From, To Clock
}

func (w Window) Contains(t time.Time) bool {
	minuteOfDay := t.Hour()*60 + t.Minute()
	if w.From.Minutes == w.To.Minutes {
		return true
	}
	if w.From.Minutes < w.To.Minutes {
		return minuteOfDay >= w.From.Minutes && minuteOfDay < w.To.Minutes
	}
	return minuteOfDay >= w.From.Minutes || minuteOfDay < w.To.Minutes
}

func (w Window) NextStart(after time.Time) time.Time {
	start := time.Date(after.Year(), after.Month(), after.Day(), w.From.Minutes/60, w.From.Minutes%60, 0, 0, after.Location())
	if !start.After(after) {
		start = start.AddDate(0, 0, 1)
	}
	return start
}

type Config struct {
	Window       Window
	Interval     time.Duration
	MaxAttempts  int
	WANInterface string
	CheckURLs    []string
	UTCOffset    *int
}

func (cfg Config) Now() time.Time {
	if cfg.UTCOffset == nil {
		return time.Now()
	}
	loc := time.FixedZone(fmt.Sprintf("UTC%+d", *cfg.UTCOffset), *cfg.UTCOffset*3600)
	return time.Now().In(loc)
}

func readRawConfig(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	raw := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		raw[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}

func wanAndCheckURLs(raw map[string]string) (wanInterface string, checkURLs []string) {
	checkURLs = defaultCheckURLs
	if v := raw["check_urls"]; v != "" {
		checkURLs = splitAndTrim(v, ",")
	}
	return raw["wan_interface"], checkURLs
}

func LoadConfig(path string) (Config, error) {
	raw, err := readRawConfig(path)
	if err != nil {
		return Config{}, err
	}

	from, err := parseClock(raw["active_from"])
	if err != nil {
		return Config{}, fmt.Errorf("active_from: %w", err)
	}
	to, err := parseClock(raw["active_to"])
	if err != nil {
		return Config{}, fmt.Errorf("active_to: %w", err)
	}

	interval, err := strconv.Atoi(raw["interval_seconds"])
	if err != nil || interval <= 0 {
		return Config{}, fmt.Errorf("interval_seconds: invalid value %q", raw["interval_seconds"])
	}

	maxAttempts, err := strconv.Atoi(raw["max_attempts"])
	if err != nil || maxAttempts <= 0 {
		return Config{}, fmt.Errorf("max_attempts: invalid value %q", raw["max_attempts"])
	}

	wanInterface, urls := wanAndCheckURLs(raw)

	var utcOffset *int
	if v := raw["utc_offset"]; v != "" {
		hours, err := parseUTCOffset(v)
		if err != nil {
			return Config{}, err
		}
		utcOffset = &hours
	}

	return Config{
		Window:       Window{From: from, To: to},
		Interval:     time.Duration(interval) * time.Second,
		MaxAttempts:  maxAttempts,
		WANInterface: wanInterface,
		CheckURLs:    urls,
		UTCOffset:    utcOffset,
	}, nil
}

func LoadRuntimeOverrides(path string) (wanInterface string, checkURLs []string) {
	raw, err := readRawConfig(path)
	if err != nil {
		return "", defaultCheckURLs
	}
	return wanAndCheckURLs(raw)
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, p)
		}
	}
	return result
}
