package logger

import (
	"fmt"
	"strings"
	"time"
)

type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var minLevel = LevelInfo

func SetLevel(level string) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		minLevel = LevelDebug
	case "info":
		minLevel = LevelInfo
	case "warn", "warning":
		minLevel = LevelWarn
	case "error":
		minLevel = LevelError
	default:
		minLevel = LevelInfo
	}
}

func log(level Level, label, msg string) {
	if level < minLevel {
		return
	}
	now := time.Now()
	fmt.Println(now.Format(time.DateTime), label, msg)
	globalBuffer.add(Entry{Time: now, Level: strings.TrimSuffix(label, ":"), Message: msg})
}

func Info(msg string) {
	log(LevelInfo, "INFO:", msg)
}

func Warn(msg string) {
	log(LevelWarn, "WARN:", msg)
}

func Error(msg string) {
	log(LevelError, "ERROR:", msg)
}
